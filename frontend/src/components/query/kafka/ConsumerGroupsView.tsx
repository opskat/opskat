import { useState } from "react";
import { useTranslation } from "react-i18next";
import { GitBranch, Loader2, Trash2, Users } from "lucide-react";
import {
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@opskat/ui";
import {
  type KafkaConsumerGroup,
  type KafkaConsumerGroupDetail,
  type KafkaOffsetResetMode,
  type KafkaTabState,
  useKafkaStore,
} from "@/stores/kafkaStore";
import { EmptyState, LoadingBlock, Metric, StatusPill } from "./shared";
import { parseIntegerArray, parseRequiredNumber } from "./utils";

export function ConsumerGroupsView({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const loadConsumerGroupDetail = useKafkaStore((s) => s.loadConsumerGroupDetail);
  return (
    <div className="grid h-full grid-cols-[minmax(420px,1fr)_minmax(360px,0.9fr)]">
      <div className="min-h-0 overflow-auto border-r">
        {state.loadingGroups && !state.consumerGroups.length ? (
          <LoadingBlock />
        ) : state.consumerGroups.length === 0 ? (
          <EmptyState text={t("query.kafkaNoConsumerGroups")} />
        ) : (
          <ConsumerGroupTable
            groups={state.consumerGroups}
            selected={state.selectedGroup}
            onSelect={(group) => loadConsumerGroupDetail(tabId, group)}
          />
        )}
      </div>
      <div className="min-h-0 overflow-auto">
        <ConsumerGroupDetailPanel tabId={tabId} state={state} />
      </div>
    </div>
  );
}

function ConsumerGroupTable({
  groups,
  selected,
  onSelect,
}: {
  groups: KafkaConsumerGroup[];
  selected?: string;
  onSelect: (group: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-sm">
      <thead className="sticky top-0 bg-muted/90 text-xs text-muted-foreground backdrop-blur">
        <tr>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaGroup")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaState")}</th>
          <th className="px-3 py-2 text-right font-medium">{t("query.kafkaCoordinator")}</th>
        </tr>
      </thead>
      <tbody>
        {groups.map((group) => (
          <tr
            key={group.group}
            className={`cursor-pointer border-t hover:bg-muted/40 ${selected === group.group ? "bg-muted/60" : ""}`}
            onClick={() => onSelect(group.group)}
          >
            <td className="max-w-[420px] truncate px-3 py-2 font-mono text-xs">{group.group}</td>
            <td className="px-3 py-2">
              <StatusPill value={group.state} />
            </td>
            <td className="px-3 py-2 text-right font-mono text-xs">{group.coordinator}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function ConsumerGroupDetailPanel({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [resetOpen, setResetOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteConsumerGroup = useKafkaStore((s) => s.deleteConsumerGroup);
  if (state.loadingGroupDetail) return <LoadingBlock />;
  if (!state.selectedGroup) return <EmptyState text={t("query.kafkaSelectConsumerGroup")} />;
  const detail = state.groupDetail;
  if (!detail) return <EmptyState text={t("query.kafkaNoConsumerGroupDetail")} />;
  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center gap-2">
        <Users className="h-4 w-4 text-muted-foreground" />
        <div className="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{detail.group}</div>
        <StatusPill value={detail.state} />
        <Button variant="outline" size="sm" className="h-7 gap-1.5" onClick={() => setResetOpen(true)}>
          <GitBranch className="h-3.5 w-3.5" />
          {t("query.kafkaResetOffset")}
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-destructive hover:text-destructive"
          onClick={() => setDeleteOpen(true)}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="grid grid-cols-3 gap-2">
        <Metric label={t("query.kafkaMembers")} value={detail.members?.length || 0} />
        <Metric label={t("query.kafkaTotalLag")} value={detail.totalLag || 0} />
        <Metric label={t("query.kafkaCoordinator")} value={detail.coordinator?.nodeId ?? "-"} />
      </div>
      {detail.lagError && (
        <div className="rounded-md border border-warning/30 bg-warning/10 p-2 text-xs">{detail.lagError}</div>
      )}
      <LagTable detail={detail} />
      <ResetConsumerGroupOffsetDialog tabId={tabId} group={detail.group} open={resetOpen} onOpenChange={setResetOpen} />
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("query.kafkaDeleteConsumerGroup")}
        description={t("query.kafkaDeleteConsumerGroupConfirmDesc", { group: detail.group })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={() => deleteConsumerGroup(tabId, detail.group)}
      />
    </div>
  );
}

function ResetConsumerGroupOffsetDialog({
  tabId,
  group,
  open,
  onOpenChange,
}: {
  tabId: string;
  group: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [topic, setTopic] = useState("");
  const [partitions, setPartitions] = useState("");
  const [mode, setMode] = useState<KafkaOffsetResetMode>("latest");
  const [offset, setOffset] = useState("");
  const [timestampMillis, setTimestampMillis] = useState("");
  const resetConsumerGroupOffset = useKafkaStore((s) => s.resetConsumerGroupOffset);
  const state = useKafkaStore((s) => s.states[tabId]);

  const confirm = async () => {
    await resetConsumerGroupOffset(tabId, {
      group,
      topic: topic.trim(),
      partitions: parseIntegerArray(partitions),
      mode,
      offset: mode === "offset" ? parseRequiredNumber(offset) : undefined,
      timestampMillis: mode === "timestamp" ? parseRequiredNumber(timestampMillis) : undefined,
    });
    onOpenChange(false);
  };

  const modeValue = mode === "timestamp" ? timestampMillis : offset;
  const canSubmit =
    topic.trim() &&
    (mode === "offset" ? offset.trim() : true) &&
    (mode === "timestamp" ? timestampMillis.trim() : true);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaResetOffset")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="rounded-md bg-muted/40 px-3 py-2 font-mono text-xs">{group}</div>
          <Input
            className="h-8 font-mono text-xs"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            placeholder={t("query.kafkaTopic")}
          />
          <Input
            className="h-8 font-mono text-xs"
            value={partitions}
            onChange={(e) => setPartitions(e.target.value)}
            placeholder={t("query.kafkaPartitionsPlaceholder")}
          />
          <div className="grid gap-2 md:grid-cols-2">
            <Select value={mode} onValueChange={(next) => setMode(next as KafkaOffsetResetMode)}>
              <SelectTrigger className="h-8">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="earliest">{t("query.kafkaOffsetEarliest")}</SelectItem>
                <SelectItem value="latest">{t("query.kafkaOffsetLatest")}</SelectItem>
                <SelectItem value="offset">Offset</SelectItem>
                <SelectItem value="timestamp">{t("query.kafkaStartTimestamp")}</SelectItem>
              </SelectContent>
            </Select>
            <Input
              className="h-8 font-mono text-xs"
              value={modeValue}
              onChange={(e) => (mode === "timestamp" ? setTimestampMillis(e.target.value) : setOffset(e.target.value))}
              disabled={mode === "earliest" || mode === "latest"}
              placeholder={mode === "timestamp" ? t("query.kafkaTimestampMillis") : t("query.kafkaOffset")}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.groupAdminLoading || !canSubmit} onClick={() => setConfirmOpen(true)}>
            {state?.groupAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaResetOffset")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("query.kafkaResetOffset")}
          description={t("query.kafkaResetOffsetConfirmDesc", { group, topic: topic.trim(), mode })}
          cancelText={t("action.cancel")}
          confirmText={t("query.kafkaResetOffset")}
          onConfirm={confirm}
        />
      </DialogContent>
    </Dialog>
  );
}

function LagTable({ detail }: { detail: KafkaConsumerGroupDetail }) {
  const { t } = useTranslation();
  const rows = detail.lag || [];
  if (!rows.length) return <EmptyState text={t("query.kafkaNoLag")} />;
  return (
    <div className="overflow-hidden rounded-md border">
      <table className="w-full text-sm">
        <thead className="bg-muted/40 text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaTopic")}</th>
            <th className="px-3 py-2 text-right font-medium">P</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaCommittedOffset")}</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaEndOffset")}</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaLag")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={`${row.topic}:${row.partition}`} className="border-t">
              <td className="max-w-[260px] truncate px-3 py-2 font-mono text-xs">{row.topic}</td>
              <td className="px-3 py-2 text-right font-mono text-xs">{row.partition}</td>
              <td className="px-3 py-2 text-right font-mono text-xs">{row.committedOffset}</td>
              <td className="px-3 py-2 text-right font-mono text-xs">{row.endOffset}</td>
              <td className="px-3 py-2 text-right font-mono text-xs">{row.lag}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
