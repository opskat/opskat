import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, Plus, Settings, Trash2 } from "lucide-react";
import {
  Button,
  Checkbox,
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
  type KafkaConnectorConfigRequest,
  type KafkaConnectorDetail,
  type KafkaConnectorSummary,
  type KafkaTabState,
  useKafkaStore,
} from "@/stores/kafkaStore";
import { EmptyState, LoadingBlock, Metric, StatusPill } from "./shared";
import { formatConnectorConfig, parseConnectorConfigObject, formatConnectorTaskSummary, errorMessage } from "./utils";

export function KafkaConnectView({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const loadConnectClusters = useKafkaStore((s) => s.loadConnectClusters);
  const loadConnectors = useKafkaStore((s) => s.loadConnectors);
  const loadConnectorDetail = useKafkaStore((s) => s.loadConnectorDetail);
  const selectedCluster = state.selectedConnectCluster || state.connectClusters[0]?.name || "";

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2">
        <div className="w-60">
          <Select
            value={selectedCluster}
            onValueChange={(next) => {
              if (next) loadConnectors(tabId, next);
            }}
            disabled={!state.connectClusters.length}
          >
            <SelectTrigger className="h-8 text-xs">
              <SelectValue placeholder={t("query.kafkaConnectCluster")} />
            </SelectTrigger>
            <SelectContent>
              {state.connectClusters.map((cluster) => (
                <SelectItem key={cluster.name} value={cluster.name}>
                  {cluster.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <Button variant="outline" size="sm" className="h-8" onClick={() => loadConnectClusters(tabId)}>
          {state.loadingConnectClusters ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : null}
          {t("query.refreshTree")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="h-8 gap-1.5"
          disabled={!selectedCluster}
          onClick={() => setCreateOpen(true)}
        >
          <Plus className="h-3.5 w-3.5" />
          {t("query.kafkaCreateConnector")}
        </Button>
        <span className="ml-auto text-xs text-muted-foreground">
          {t("query.kafkaConnectorTotal", { count: state.connectors.length })}
        </span>
      </div>
      {!state.connectClusters.length && !state.loadingConnectClusters ? (
        <EmptyState text={t("query.kafkaNoConnectClusters")} />
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-[minmax(340px,0.8fr)_minmax(480px,1.2fr)]">
          <div className="min-h-0 overflow-auto border-r">
            {state.loadingConnectors && !state.connectors.length ? (
              <LoadingBlock />
            ) : state.connectors.length === 0 ? (
              <EmptyState text={t("query.kafkaNoConnectors")} />
            ) : (
              <ConnectorTable
                connectors={state.connectors}
                selected={state.selectedConnector}
                onSelect={(name) => loadConnectorDetail(tabId, name)}
              />
            )}
          </div>
          <div className="min-h-0 overflow-auto">
            <ConnectorDetailPanel tabId={tabId} state={state} />
          </div>
        </div>
      )}
      <CreateConnectorDialog tabId={tabId} open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  );
}

function ConnectorTable({
  connectors,
  selected,
  onSelect,
}: {
  connectors: KafkaConnectorSummary[];
  selected?: string;
  onSelect: (name: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <table className="w-full text-sm">
      <thead className="sticky top-0 bg-muted/90 text-xs text-muted-foreground backdrop-blur">
        <tr>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaConnector")}</th>
          <th className="px-3 py-2 text-left font-medium">{t("query.kafkaState")}</th>
          <th className="px-3 py-2 text-right font-medium">{t("query.kafkaConnectorTasks")}</th>
        </tr>
      </thead>
      <tbody>
        {connectors.map((connector) => (
          <tr
            key={connector.name}
            className={`cursor-pointer border-t hover:bg-muted/40 ${selected === connector.name ? "bg-muted/60" : ""}`}
            onClick={() => onSelect(connector.name)}
          >
            <td className="max-w-[320px] truncate px-3 py-2 font-mono text-xs">{connector.name}</td>
            <td className="px-3 py-2">
              <div className="flex items-center gap-1.5">
                <StatusPill value={connector.status} />
                {connector.type && <StatusPill value={connector.type} />}
              </div>
            </td>
            <td className="px-3 py-2 text-right font-mono text-xs text-muted-foreground">
              {formatConnectorTaskSummary(connector)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function ConnectorDetailPanel({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [updateOpen, setUpdateOpen] = useState(false);
  const [restartOpen, setRestartOpen] = useState(false);
  const [pauseOpen, setPauseOpen] = useState(false);
  const [resumeOpen, setResumeOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const pauseConnector = useKafkaStore((s) => s.pauseConnector);
  const resumeConnector = useKafkaStore((s) => s.resumeConnector);
  const deleteConnector = useKafkaStore((s) => s.deleteConnector);

  if (state.loadingConnectorDetail) return <LoadingBlock />;
  if (!state.selectedConnector) return <EmptyState text={t("query.kafkaSelectConnector")} />;
  const detail = state.connectorDetail;
  if (!detail) return <EmptyState text={t("query.kafkaNoConnectorDetail")} />;

  const connectorState = detail.status?.connector?.state;
  const tasks = detail.status?.tasks || [];

  return (
    <div className="space-y-4 p-4">
      <div className="flex items-center gap-2">
        <Settings className="h-4 w-4 text-muted-foreground" />
        <div className="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{detail.name}</div>
        <StatusPill value={detail.type || detail.status?.type} />
        <StatusPill value={connectorState} />
        <Button variant="outline" size="sm" className="h-7" onClick={() => setUpdateOpen(true)}>
          {t("query.kafkaUpdateConnectorConfig")}
        </Button>
        <Button variant="outline" size="sm" className="h-7" onClick={() => setPauseOpen(true)}>
          {t("query.kafkaPauseConnector")}
        </Button>
        <Button variant="outline" size="sm" className="h-7" onClick={() => setResumeOpen(true)}>
          {t("query.kafkaResumeConnector")}
        </Button>
        <Button variant="outline" size="sm" className="h-7" onClick={() => setRestartOpen(true)}>
          {t("query.kafkaRestartConnector")}
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
        <Metric label={t("query.kafkaConnectCluster")} value={state.selectedConnectCluster || "-"} />
        <Metric label={t("query.kafkaConnectorTasks")} value={tasks.length || detail.tasks?.length || 0} />
        <Metric label={t("query.kafkaConnectorState")} value={connectorState || "-"} />
      </div>
      <ConnectorTasksTable detail={detail} />
      <ConnectorConfigTable config={detail.config || {}} />
      <ConnectorConfigDialog
        tabId={tabId}
        detail={detail}
        mode="update"
        open={updateOpen}
        onOpenChange={setUpdateOpen}
      />
      <RestartConnectorDialog tabId={tabId} name={detail.name} open={restartOpen} onOpenChange={setRestartOpen} />
      <ConfirmDialog
        open={pauseOpen}
        onOpenChange={setPauseOpen}
        title={t("query.kafkaPauseConnector")}
        description={t("query.kafkaPauseConnectorConfirmDesc", { name: detail.name })}
        cancelText={t("action.cancel")}
        confirmText={t("query.kafkaPauseConnector")}
        onConfirm={async () => {
          await pauseConnector(tabId, detail.name);
          setPauseOpen(false);
        }}
      />
      <ConfirmDialog
        open={resumeOpen}
        onOpenChange={setResumeOpen}
        title={t("query.kafkaResumeConnector")}
        description={t("query.kafkaResumeConnectorConfirmDesc", { name: detail.name })}
        cancelText={t("action.cancel")}
        confirmText={t("query.kafkaResumeConnector")}
        onConfirm={async () => {
          await resumeConnector(tabId, detail.name);
          setResumeOpen(false);
        }}
      />
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("query.kafkaDeleteConnector")}
        description={t("query.kafkaDeleteConnectorConfirmDesc", { name: detail.name })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={async () => {
          await deleteConnector(tabId, detail.name);
          setDeleteOpen(false);
        }}
      />
    </div>
  );
}

function ConnectorTasksTable({ detail }: { detail: KafkaConnectorDetail }) {
  const { t } = useTranslation();
  const rows = detail.status?.tasks || [];
  if (!rows.length) return <EmptyState text={t("query.kafkaNoConnectorTasks")} />;
  return (
    <div className="overflow-hidden rounded-md border">
      <table className="w-full text-sm">
        <thead className="bg-muted/40 text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 text-right font-medium">ID</th>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaState")}</th>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaConnectorWorker")}</th>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaConnectorTrace")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((task) => (
            <tr key={task.id} className="border-t">
              <td className="px-3 py-2 text-right font-mono text-xs">{task.id}</td>
              <td className="px-3 py-2">
                <StatusPill value={task.state} />
              </td>
              <td className="max-w-[220px] truncate px-3 py-2 font-mono text-xs">{task.workerId || "-"}</td>
              <td className="max-w-[360px] truncate px-3 py-2 font-mono text-xs text-muted-foreground">
                {task.trace || "-"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ConnectorConfigTable({ config }: { config: Record<string, string> }) {
  const { t } = useTranslation();
  const entries = Object.entries(config).sort(([a], [b]) => a.localeCompare(b));
  if (!entries.length) return <EmptyState text={t("query.kafkaNoConnectorConfig")} />;
  return (
    <div className="overflow-hidden rounded-md border">
      <div className="border-b px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {t("query.kafkaConnectorConfig")}
      </div>
      <table className="w-full text-sm">
        <tbody>
          {entries.map(([key, value]) => (
            <tr key={key} className="border-t first:border-t-0">
              <td className="w-64 max-w-[260px] truncate bg-muted/30 px-3 py-2 font-mono text-xs">{key}</td>
              <td className="break-all px-3 py-2 font-mono text-xs">{value}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CreateConnectorDialog({
  tabId,
  open,
  onOpenChange,
}: {
  tabId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return <ConnectorConfigDialog tabId={tabId} mode="create" open={open} onOpenChange={onOpenChange} />;
}

interface ConnectorFormState {
  sourceKey: string;
  name: string;
  config: string;
  formError: string | null;
}

function getConnectorFormSourceKey(mode: "create" | "update", detail?: KafkaConnectorDetail) {
  return `${mode}:${detail?.name || ""}:${JSON.stringify(detail?.config || {})}`;
}

function createConnectorFormState(sourceKey: string, detail?: KafkaConnectorDetail): ConnectorFormState {
  return {
    sourceKey,
    name: detail?.name || "",
    config: formatConnectorConfig(detail?.config),
    formError: null,
  };
}

function ConnectorConfigDialog({
  tabId,
  detail,
  mode,
  open,
  onOpenChange,
}: {
  tabId: string;
  detail?: KafkaConnectorDetail;
  mode: "create" | "update";
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const createConnector = useKafkaStore((s) => s.createConnector);
  const updateConnectorConfig = useKafkaStore((s) => s.updateConnectorConfig);
  const state = useKafkaStore((s) => s.states[tabId]);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const formSourceKey = getConnectorFormSourceKey(mode, detail);
  const initialForm = createConnectorFormState(formSourceKey, detail);
  const [formState, setFormState] = useState(initialForm);
  const form = formState.sourceKey === formSourceKey ? formState : initialForm;
  const { name, config, formError } = form;

  const updateForm = (patch: Partial<Omit<ConnectorFormState, "sourceKey">>) => {
    setFormState((current) => ({
      ...(current.sourceKey === formSourceKey ? current : initialForm),
      ...patch,
      sourceKey: formSourceKey,
    }));
  };

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      setConfirmOpen(false);
      setFormState(initialForm);
    }
    onOpenChange(nextOpen);
  };

  const canSubmit = name.trim() && config.trim();

  const submit = async () => {
    updateForm({ formError: null });
    let parsedConfig: Record<string, string>;
    try {
      parsedConfig = parseConnectorConfigObject(config);
    } catch (err) {
      setConfirmOpen(false);
      updateForm({ formError: errorMessage(err) });
      return;
    }
    const req: KafkaConnectorConfigRequest = {
      name: name.trim(),
      config: parsedConfig,
    };
    try {
      if (mode === "create") {
        await createConnector(tabId, req);
      } else {
        await updateConnectorConfig(tabId, req);
      }
      handleOpenChange(false);
    } catch (err) {
      setConfirmOpen(false);
      updateForm({ formError: errorMessage(err) });
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {mode === "create" ? t("query.kafkaCreateConnector") : t("query.kafkaUpdateConnectorConfig")}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Input
            className="h-8 font-mono text-xs"
            value={name}
            onChange={(e) => updateForm({ name: e.target.value })}
            disabled={mode === "update"}
            placeholder={t("query.kafkaConnector")}
          />
          <Textarea
            className="min-h-80 font-mono text-xs"
            value={config}
            onChange={(e) => updateForm({ config: e.target.value })}
            placeholder={t("query.kafkaConnectorConfigPlaceholder")}
          />
          {formError && (
            <div className="flex items-center gap-1.5 text-xs text-destructive">
              <AlertCircle className="h-3.5 w-3.5 shrink-0" />
              <span className="break-all">{formError}</span>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.connectAdminLoading || !canSubmit} onClick={() => setConfirmOpen(true)}>
            {state?.connectAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {mode === "create" ? t("query.kafkaCreateConnector") : t("query.kafkaUpdateConnectorConfig")}
          </Button>
        </DialogFooter>
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={mode === "create" ? t("query.kafkaCreateConnector") : t("query.kafkaUpdateConnectorConfig")}
          description={
            mode === "create"
              ? t("query.kafkaCreateConnectorConfirmDesc", { name: name.trim() })
              : t("query.kafkaUpdateConnectorConfigConfirmDesc", { name: name.trim() })
          }
          cancelText={t("action.cancel")}
          confirmText={mode === "create" ? t("query.kafkaCreateConnector") : t("query.kafkaUpdateConnectorConfig")}
          onConfirm={submit}
        />
      </DialogContent>
    </Dialog>
  );
}

function RestartConnectorDialog({
  tabId,
  name,
  open,
  onOpenChange,
}: {
  tabId: string;
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const restartConnector = useKafkaStore((s) => s.restartConnector);
  const state = useKafkaStore((s) => s.states[tabId]);
  const [includeTasks, setIncludeTasks] = useState(false);
  const [onlyFailed, setOnlyFailed] = useState(false);

  const submit = async () => {
    await restartConnector(tabId, name, includeTasks, onlyFailed);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("query.kafkaRestartConnector")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="rounded-md bg-muted/40 px-3 py-2 font-mono text-xs">{name}</div>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={includeTasks}
              onCheckedChange={(checked) => {
                const next = checked === true;
                setIncludeTasks(next);
                if (!next) setOnlyFailed(false);
              }}
            />
            {t("query.kafkaRestartConnectorTasks")}
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={onlyFailed}
              disabled={!includeTasks}
              onCheckedChange={(checked) => setOnlyFailed(checked === true)}
            />
            {t("query.kafkaRestartConnectorOnlyFailed")}
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button disabled={state?.connectAdminLoading} onClick={submit}>
            {state?.connectAdminLoading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
            {t("query.kafkaRestartConnector")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
