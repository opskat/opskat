import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Database, GitBranch, Loader2, Plus, RefreshCw, Search, Send, Settings, Trash2 } from "lucide-react";
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
  Textarea,
} from "@opskat/ui";
import {
  type KafkaDeleteRecordsPartition,
  type KafkaMessageStartMode,
  type KafkaPayloadEncoding,
  type KafkaRecord,
  type KafkaTabState,
  type KafkaTopicSummary,
  useKafkaStore,
} from "@/stores/kafkaStore";
import { EmptyState, LoadingBlock, Metric, StatusPill } from "./shared";
import { parseOptionalJsonObject, parseConfigUpdates } from "./utils";

export function TopicsView({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const setTopicSearch = useKafkaStore((s) => s.setTopicSearch);
  const setIncludeInternal = useKafkaStore((s) => s.setIncludeInternal);
  const loadTopics = useKafkaStore((s) => s.loadTopics);
  const loadTopicDetail = useKafkaStore((s) => s.loadTopicDetail);

  const applySearch = () => loadTopics(tabId);

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2">
        <div className="relative w-80 max-w-[50vw]">
          <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="h-8 pl-7 text-sm"
            value={state.topicSearch}
            onChange={(e) => setTopicSearch(tabId, e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") applySearch();
            }}
            placeholder={t("query.kafkaFilterTopics")}
          />
        </div>
        <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <input
            type="checkbox"
            checked={state.includeInternal}
            onChange={(e) => {
              setIncludeInternal(tabId, e.target.checked);
              setTimeout(() => loadTopics(tabId), 0);
            }}
          />
          {t("query.kafkaIncludeInternal")}
        </label>
        <Button variant="outline" size="sm" className="h-8" onClick={applySearch}>
          {t("query.applyFilter")}
        </Button>
        <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3.5 w-3.5" />
          {t("query.kafkaCreateTopic")}
        </Button>
        <span className="ml-auto text-xs text-muted-foreground">
          {t("query.kafkaTopicTotal", { count: state.topicsTotal })}
        </span>
      </div>
      <div className="grid min-h-0 flex-1 grid-cols-[minmax(420px,1fr)_minmax(360px,0.9fr)]">
        <div className="min-h-0 overflow-auto border-r">
          {state.loadingTopics && !state.topics.length ? (
            <LoadingBlock />
          ) : state.topics.length === 0 ? (
            <EmptyState text={t("query.kafkaNoTopics")} />
          ) : (
            <TopicTable
              topics={state.topics}
              selected={state.selectedTopic}
              onSelect={(topic) => loadTopicDetail(tabId, topic)}
            />
          )}
        </div>
        <div className="min-h-0 overflow-auto">
          <TopicDetailPanel tabId={tabId} state={state} />
        </div>
      </div>
      <CreateTopicDialog tabId={tabId} open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  );
}

function CreateTopicDialog({
  tabId,
  open,
  onOpenChange,
}: {
  tabId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const createTopic = useKafkaStore((s) => s.createTopic);
  const state = useKafkaStore((s) => s.states[tabId]);
  const [topic, setTopic] = useState("");
  const [partitions, setPartitions] = useState(1);
  const [replicationFactor, setReplicationFactor] = useState(1);
  const [configs, setConfigs] = useState("");

  const submit = async () => {
    const name = topic.trim();
    if (!name) return;
    await createTopic(tabId, {
      topic: name,
      partitions,
      replicationFactor,
      configs: parseOptionalJsonObject(configs),
    });
    setTopic("");
    setConfigs("");
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaCreateTopic")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Input
            className="font-mono text-sm"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            placeholder={t("query.kafkaTopic")}
          />
          <div className="grid grid-cols-2 gap-2">
            <NumberInput value={partitions} onChange={setPartitions} placeholder={t("query.kafkaPartitions")} />
            <NumberInput
              value={replicationFactor}
              onChange={setReplicationFactor}
              placeholder={t("query.kafkaReplicationFactor")}
            />
          </div>
          <Textarea
            className="min-h-24 font-mono text-xs"
            value={configs}
            onChange={(e) => setConfigs(e.target.value)}
            placeholder={t("query.kafkaConfigsPlaceholder")}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.topicAdminLoading || !topic.trim()} onClick={submit}>
            {state?.topicAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaCreateTopic")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TopicTable({
  topics,
  selected,
  onSelect,
}: {
  topics: KafkaTopicSummary[];
  selected?: string;
  onSelect: (topic: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-sm">
      <thead className="sticky top-0 bg-muted/90 text-xs text-muted-foreground backdrop-blur">
        <tr>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaTopic")}</th>
          <th className="px-3 py-2 text-right font-medium">{t("query.kafkaPartitions")}</th>
          <th className="px-3 py-2 text-right font-medium">RF</th>
          <th className="px-3 py-2 text-center font-medium">{t("query.kafkaInternal")}</th>
        </tr>
      </thead>
      <tbody>
        {topics.map((topic) => (
          <tr
            key={topic.name}
            className={`cursor-pointer border-t hover:bg-muted/40 ${selected === topic.name ? "bg-muted/60" : ""}`}
            onClick={() => onSelect(topic.name)}
          >
            <td className="max-w-[420px] truncate px-3 py-2 font-mono text-xs">{topic.name}</td>
            <td className="px-3 py-2 text-right tabular-nums">{topic.partitionCount}</td>
            <td className="px-3 py-2 text-right tabular-nums">{topic.replicationFactor}</td>
            <td className="px-3 py-2 text-center">{topic.internal ? "yes" : "-"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function TopicDetailPanel({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [configOpen, setConfigOpen] = useState(false);
  const [partitionsOpen, setPartitionsOpen] = useState(false);
  const deleteTopic = useKafkaStore((s) => s.deleteTopic);
  if (state.loadingTopicDetail) return <LoadingBlock />;
  if (!state.selectedTopic) return <EmptyState text={t("query.kafkaSelectTopic")} />;
  const detail = state.topicDetail;
  if (!detail) return <EmptyState text={t("query.kafkaNoTopicDetail")} />;
  return (
    <div className="space-y-4 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Database className="h-4 w-4 text-muted-foreground" />
        <div className="min-w-[220px] flex-1 truncate font-mono text-sm font-semibold">{detail.name}</div>
        {detail.internal && <StatusPill value="internal" />}
        <Button variant="outline" size="sm" className="h-7 gap-1.5" onClick={() => setConfigOpen(true)}>
          <Settings className="h-3.5 w-3.5" />
          {t("query.kafkaUpdateConfig")}
        </Button>
        <Button variant="outline" size="sm" className="h-7 gap-1.5" onClick={() => setPartitionsOpen(true)}>
          <GitBranch className="h-3.5 w-3.5" />
          {t("query.kafkaIncreasePartitions")}
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
        <Metric label={t("query.kafkaPartitions")} value={detail.partitionCount} />
        <Metric label={t("query.kafkaReplicationFactor")} value={detail.replicationFactor} />
        <Metric label={t("query.kafkaUnderReplicated")} value={detail.underReplicatedPartitionCount} />
      </div>
      <div className="overflow-hidden rounded-md border">
        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 text-right font-medium">P</th>
              <th className="px-3 py-2 text-right font-medium">Leader</th>
              <th className="px-3 py-2 text-left font-medium">Replicas</th>
              <th className="px-3 py-2 text-left font-medium">ISR</th>
            </tr>
          </thead>
          <tbody>
            {(detail.partitions || []).map((partition) => (
              <tr key={partition.partition} className="border-t">
                <td className="px-3 py-2 text-right font-mono text-xs">{partition.partition}</td>
                <td className="px-3 py-2 text-right font-mono text-xs">{partition.leader}</td>
                <td className="px-3 py-2 font-mono text-xs">{partition.replicas?.join(", ") || "-"}</td>
                <td className="px-3 py-2 font-mono text-xs">{partition.isr?.join(", ") || "-"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <MessageBrowser tabId={tabId} state={state} />
      <ProduceMessagePanel tabId={tabId} state={state} />
      <AlterTopicConfigDialog tabId={tabId} topic={detail.name} open={configOpen} onOpenChange={setConfigOpen} />
      <IncreasePartitionsDialog
        tabId={tabId}
        topic={detail.name}
        currentCount={detail.partitionCount}
        open={partitionsOpen}
        onOpenChange={setPartitionsOpen}
      />
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("query.kafkaDeleteTopic")}
        description={t("query.kafkaDeleteTopicConfirmDesc", { topic: detail.name })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={() => deleteTopic(tabId, detail.name)}
      />
    </div>
  );
}

function AlterTopicConfigDialog({
  tabId,
  topic,
  open,
  onOpenChange,
}: {
  tabId: string;
  topic: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [updatesText, setUpdatesText] = useState("");
  const alterTopicConfig = useKafkaStore((s) => s.alterTopicConfig);
  const state = useKafkaStore((s) => s.states[tabId]);

  const confirm = async () => {
    const updates = parseConfigUpdates(updatesText);
    await alterTopicConfig(tabId, topic, updates);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaUpdateConfig")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="rounded-md bg-muted/40 px-3 py-2 font-mono text-xs">{topic}</div>
          <Textarea
            className="min-h-40 font-mono text-xs"
            value={updatesText}
            onChange={(e) => setUpdatesText(e.target.value)}
            placeholder={t("query.kafkaConfigUpdatesPlaceholder")}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.topicAdminLoading || !updatesText.trim()} onClick={() => setConfirmOpen(true)}>
            {state?.topicAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("action.save")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("query.kafkaUpdateConfig")}
          description={t("query.kafkaUpdateConfigConfirmDesc", { topic })}
          cancelText={t("action.cancel")}
          confirmText={t("action.save")}
          onConfirm={confirm}
        />
      </DialogContent>
    </Dialog>
  );
}

function IncreasePartitionsDialog({
  tabId,
  topic,
  currentCount,
  open,
  onOpenChange,
}: {
  tabId: string;
  topic: string;
  currentCount: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [nextCountState, setNextCountState] = useState({
    currentCount,
    nextCount: currentCount + 1,
  });
  const nextCount = nextCountState.currentCount === currentCount ? nextCountState.nextCount : currentCount + 1;
  const increasePartitions = useKafkaStore((s) => s.increasePartitions);
  const state = useKafkaStore((s) => s.states[tabId]);

  const confirm = async () => {
    await increasePartitions(tabId, topic, nextCount);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaIncreasePartitions")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="rounded-md bg-muted/40 px-3 py-2 font-mono text-xs">{topic}</div>
          <Metric label={t("query.kafkaCurrentPartitions")} value={currentCount} />
          <NumberInput
            value={nextCount}
            onChange={(value) => setNextCountState({ currentCount, nextCount: value })}
            placeholder={t("query.kafkaPartitions")}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.topicAdminLoading || nextCount <= currentCount} onClick={() => setConfirmOpen(true)}>
            {state?.topicAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaIncreasePartitions")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("query.kafkaIncreasePartitions")}
          description={t("query.kafkaIncreasePartitionsConfirmDesc", { topic, count: nextCount })}
          cancelText={t("action.cancel")}
          confirmText={t("query.kafkaIncreasePartitions")}
          onConfirm={confirm}
        />
      </DialogContent>
    </Dialog>
  );
}

function MessageBrowser({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [deleteRecordsOpen, setDeleteRecordsOpen] = useState(false);
  const setMessageBrowser = useKafkaStore((s) => s.setMessageBrowser);
  const browseMessages = useKafkaStore((s) => s.browseMessages);
  const browser = state.messageBrowser;
  const records = browser.response?.records || [];

  return (
    <div className="overflow-hidden rounded-md border">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t("query.kafkaMessages")}
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-7 gap-1.5" onClick={() => browseMessages(tabId)}>
            {state.loadingMessages ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <RefreshCw className="h-3.5 w-3.5" />
            )}
            {t("query.kafkaBrowseMessages")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-1.5 text-destructive hover:text-destructive"
            onClick={() => setDeleteRecordsOpen(true)}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {t("query.kafkaDeleteRecords")}
          </Button>
        </div>
      </div>
      <div className="grid gap-2 border-b bg-muted/20 p-3 text-xs md:grid-cols-6">
        <Input
          className="h-8 font-mono"
          value={browser.partition}
          onChange={(e) => setMessageBrowser(tabId, { partition: e.target.value })}
          placeholder={t("query.kafkaAllPartitions")}
        />
        <Select
          value={browser.startMode}
          onValueChange={(value) => setMessageBrowser(tabId, { startMode: value as KafkaMessageStartMode })}
        >
          <SelectTrigger className="h-8">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="newest">{t("query.kafkaStartNewest")}</SelectItem>
            <SelectItem value="oldest">{t("query.kafkaStartOldest")}</SelectItem>
            <SelectItem value="offset">{t("query.kafkaStartOffset")}</SelectItem>
            <SelectItem value="timestamp">{t("query.kafkaStartTimestamp")}</SelectItem>
          </SelectContent>
        </Select>
        <Input
          className="h-8 font-mono"
          value={browser.startMode === "timestamp" ? browser.timestampMillis : browser.offset}
          onChange={(e) =>
            setMessageBrowser(
              tabId,
              browser.startMode === "timestamp" ? { timestampMillis: e.target.value } : { offset: e.target.value }
            )
          }
          disabled={browser.startMode === "newest" || browser.startMode === "oldest"}
          placeholder={browser.startMode === "timestamp" ? t("query.kafkaTimestampMillis") : t("query.kafkaOffset")}
        />
        <NumberInput
          value={browser.limit}
          onChange={(value) => setMessageBrowser(tabId, { limit: value })}
          placeholder={t("query.kafkaLimit")}
        />
        <NumberInput
          value={browser.maxBytes}
          onChange={(value) => setMessageBrowser(tabId, { maxBytes: value })}
          placeholder={t("query.kafkaMaxBytes")}
        />
        <Select
          value={browser.decodeMode}
          onValueChange={(value) => setMessageBrowser(tabId, { decodeMode: value as KafkaPayloadEncoding })}
        >
          <SelectTrigger className="h-8">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="text">text</SelectItem>
            <SelectItem value="json">json</SelectItem>
            <SelectItem value="hex">hex</SelectItem>
            <SelectItem value="base64">base64</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {browser.response?.errors?.length ? (
        <div className="border-b bg-warning/10 px-3 py-2 text-xs text-warning">
          {browser.response.errors.join("; ")}
        </div>
      ) : null}
      {state.loadingMessages && !records.length ? (
        <LoadingBlock />
      ) : records.length === 0 ? (
        <EmptyState text={t("query.kafkaNoMessages")} />
      ) : (
        <MessageTable records={records} />
      )}
      <DeleteRecordsDialog
        tabId={tabId}
        topic={state.selectedTopic || ""}
        open={deleteRecordsOpen}
        onOpenChange={setDeleteRecordsOpen}
      />
    </div>
  );
}

function DeleteRecordsDialog({
  tabId,
  topic,
  open,
  onOpenChange,
}: {
  tabId: string;
  topic: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [partition, setPartition] = useState("");
  const [offset, setOffset] = useState("");
  const deleteTopicRecords = useKafkaStore((s) => s.deleteTopicRecords);
  const state = useKafkaStore((s) => s.states[tabId]);

  const partitionValue = Number(partition);
  const offsetValue = Number(offset);
  const canSubmit =
    Number.isInteger(partitionValue) && partitionValue >= 0 && Number.isInteger(offsetValue) && offsetValue >= 0;

  const confirm = async () => {
    const partitions: KafkaDeleteRecordsPartition[] = [{ partition: partitionValue, offset: offsetValue }];
    await deleteTopicRecords(tabId, topic, partitions);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaDeleteRecords")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="rounded-md bg-muted/40 px-3 py-2 font-mono text-xs">{topic}</div>
          <Input
            className="h-8 font-mono text-xs"
            value={partition}
            onChange={(e) => setPartition(e.target.value)}
            placeholder={t("query.kafkaPartition")}
          />
          <Input
            className="h-8 font-mono text-xs"
            value={offset}
            onChange={(e) => setOffset(e.target.value)}
            placeholder={t("query.kafkaDeleteBeforeOffset")}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button
            disabled={state?.topicAdminLoading || !canSubmit}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            onClick={() => setConfirmOpen(true)}
          >
            {state?.topicAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaDeleteRecords")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("query.kafkaDeleteRecords")}
          description={t("query.kafkaDeleteRecordsConfirmDesc", {
            topic,
            partition: partitionValue,
            offset: offsetValue,
          })}
          cancelText={t("action.cancel")}
          confirmText={t("action.delete")}
          onConfirm={confirm}
        />
      </DialogContent>
    </Dialog>
  );
}

function NumberInput({
  value,
  onChange,
  placeholder,
}: {
  value: number;
  onChange: (value: number) => void;
  placeholder: string;
}) {
  return (
    <Input
      className="h-8 font-mono"
      type="number"
      value={value}
      min={1}
      onChange={(e) => onChange(Number(e.target.value) || 1)}
      placeholder={placeholder}
    />
  );
}

function MessageTable({ records }: { records: KafkaRecord[] }) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-xs">
      <thead className="bg-muted/40 text-muted-foreground">
        <tr>
          <th className="px-3 py-2 text-right font-medium">P</th>
          <th className="px-3 py-2 text-right font-medium">Offset</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaMessageKey")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaMessageValue")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaHeaders")}</th>
        </tr>
      </thead>
      <tbody>
        {records.map((record) => (
          <tr key={`${record.partition}:${record.offset}`} className="border-t align-top">
            <td className="px-3 py-2 text-right font-mono">{record.partition}</td>
            <td className="px-3 py-2 text-right font-mono">
              <div>{record.offset}</div>
              <div className="mt-1 text-[10px] text-muted-foreground">{record.timestamp}</div>
            </td>
            <td className="max-w-[180px] px-3 py-2">
              <PayloadPreview
                value={record.key}
                bytes={record.keyBytes}
                encoding={record.keyEncoding}
                truncated={record.keyTruncated}
              />
            </td>
            <td className="max-w-[260px] px-3 py-2">
              <PayloadPreview
                value={record.value}
                bytes={record.valueBytes}
                encoding={record.valueEncoding}
                truncated={record.valueTruncated}
              />
            </td>
            <td className="max-w-[180px] px-3 py-2">
              {record.headers?.length ? (
                <div className="space-y-1">
                  {record.headers.map((header, index) => (
                    <div key={`${header.key}:${index}`} className="min-w-0">
                      <span className="font-mono text-muted-foreground">{header.key}</span>
                      <PayloadPreview
                        value={header.value}
                        bytes={header.valueBytes}
                        encoding={header.valueEncoding}
                        truncated={header.valueTruncated}
                      />
                    </div>
                  ))}
                </div>
              ) : (
                <span className="text-muted-foreground">-</span>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function PayloadPreview({
  value,
  bytes,
  encoding,
  truncated,
}: {
  value?: string;
  bytes: number;
  encoding: string;
  truncated: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="min-w-0">
      <div className="mb-1 flex flex-wrap items-center gap-1 text-[10px] text-muted-foreground">
        <span>{encoding}</span>
        <span>{bytes}B</span>
        {truncated && <span className="rounded border px-1 text-[9px]">{t("query.kafkaTruncated")}</span>}
      </div>
      <pre className="max-h-24 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2 font-mono text-[11px] leading-relaxed">
        {value || "-"}
      </pre>
    </div>
  );
}

function ProduceMessagePanel({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const setProduceMessage = useKafkaStore((s) => s.setProduceMessage);
  const produceKafkaMessage = useKafkaStore((s) => s.produceKafkaMessage);
  const form = state.produceMessage;

  return (
    <div className="overflow-hidden rounded-md border">
      <div className="border-b px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {t("query.kafkaProduceMessage")}
      </div>
      <div className="space-y-3 p-3">
        <div className="grid gap-2 md:grid-cols-[120px_1fr_120px_120px]">
          <Input
            className="h-8 font-mono text-xs"
            value={form.partition}
            onChange={(e) => setProduceMessage(tabId, { partition: e.target.value })}
            placeholder={t("query.kafkaAllPartitions")}
          />
          <Input
            className="h-8 font-mono text-xs"
            value={form.key}
            onChange={(e) => setProduceMessage(tabId, { key: e.target.value })}
            placeholder={t("query.kafkaMessageKey")}
          />
          <EncodingSelect
            value={form.keyEncoding}
            onChange={(value) => setProduceMessage(tabId, { keyEncoding: value })}
          />
          <EncodingSelect
            value={form.valueEncoding}
            onChange={(value) => setProduceMessage(tabId, { valueEncoding: value })}
          />
        </div>
        <Textarea
          className="min-h-24 font-mono text-xs"
          value={form.value}
          onChange={(e) => setProduceMessage(tabId, { value: e.target.value })}
          placeholder={t("query.kafkaMessageValue")}
        />
        <Textarea
          className="min-h-16 font-mono text-xs"
          value={form.headers}
          onChange={(e) => setProduceMessage(tabId, { headers: e.target.value })}
          placeholder={t("query.kafkaHeadersPlaceholder")}
        />
        <div className="flex justify-end">
          <Button
            className="h-8 gap-1.5"
            size="sm"
            disabled={state.producingMessage}
            onClick={() => setConfirmOpen(true)}
          >
            {state.producingMessage ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Send className="h-3.5 w-3.5" />
            )}
            {t("query.kafkaSendMessage")}
          </Button>
        </div>
      </div>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("query.kafkaProduceConfirmTitle")}
        description={t("query.kafkaProduceConfirmDesc", { topic: state.selectedTopic || "" })}
        cancelText={t("action.cancel")}
        confirmText={t("query.kafkaSendMessage")}
        onConfirm={() => produceKafkaMessage(tabId)}
      />
    </div>
  );
}

function EncodingSelect({
  value,
  onChange,
}: {
  value: KafkaPayloadEncoding;
  onChange: (value: KafkaPayloadEncoding) => void;
}) {
  return (
    <Select value={value} onValueChange={(next) => onChange(next as KafkaPayloadEncoding)}>
      <SelectTrigger className="h-8 text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="text">text</SelectItem>
        <SelectItem value="json">json</SelectItem>
        <SelectItem value="hex">hex</SelectItem>
        <SelectItem value="base64">base64</SelectItem>
      </SelectContent>
    </Select>
  );
}
