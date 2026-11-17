import { useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, ChevronRight, Clock, RotateCw, Server } from "lucide-react";
import { Button } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import {
  eventsKey,
  listKey,
  listTabId,
  resourceTabId,
  typeKeyOf,
  useK8sStore,
  type K8sAttentionItem,
  type K8sClusterState,
  type K8sClusterVersion,
  type K8sResourceRow,
  type K8sTableColumn,
} from "@/stores/k8sStore";
import { K8sAsyncState } from "../K8sAsyncState";
import { K8sSectionCard } from "../K8sSectionCard";
import { getK8sStatusColor, statusVariantToClass } from "../utils";
import { K8sEventsTable } from "./K8sEventsTable";

const NODES = "/nodes";
const EVENTS = "/events";
const RECENT_EVENTS = 5;
// Health sections that feed the 需要关注 list.
const ATTENTION_SECTIONS = ["pods", "deployments", "statefulsets", "daemonsets", "jobs"];

function Stat({
  testId,
  label,
  value,
  children,
}: {
  testId: string;
  label: string;
  value?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="rounded-xl border bg-card p-3" data-testid={testId}>
      <p className="text-[11px] text-muted-foreground">{label}</p>
      {value !== undefined && <p className="mt-1 font-mono text-lg font-semibold">{value}</p>}
      {children}
    </div>
  );
}

function Num({ id, value, tone }: { id: string; value: number; tone?: string }) {
  return (
    <span data-testid={id} className={tone}>
      {value}
    </span>
  );
}

const cellOf = (columns: K8sTableColumn[], row: K8sResourceRow, name: string) => {
  const idx = columns.findIndex((c) => c.name.toLowerCase() === name);
  return idx < 0 ? "" : String(row.cells[idx] ?? "");
};

/**
 * Cluster overview for the selected namespace. Every block reads on its own — the health summary (stat cards
 * and 需要关注), the warning events, the node list — so one failing leaves the others as they are.
 */
export function K8sOverview({
  assetId,
  cluster,
  version,
}: {
  assetId: number;
  cluster: K8sClusterState;
  version: K8sClusterVersion;
}) {
  const { t } = useTranslation();
  const namespace = cluster.namespace;
  const health = useK8sStore((s) => s.health[assetId]?.[namespace]);
  const events = useK8sStore((s) => s.eventLists[assetId]?.[eventsKey(namespace, true)]);
  const nodes = useK8sStore((s) => s.lists[assetId]?.[listKey(NODES, "")]);
  const loadHealth = useK8sStore((s) => s.loadHealth);
  const loadEvents = useK8sStore((s) => s.loadEvents);
  const loadList = useK8sStore((s) => s.loadList);
  const openTab = useK8sStore((s) => s.openTab);

  useEffect(() => {
    void loadHealth(assetId, namespace);
    void loadEvents(assetId, namespace, true);
    void loadList(assetId, NODES, "");
  }, [assetId, namespace, loadHealth, loadEvents, loadList]);

  const retryHealth = () => void loadHealth(assetId, namespace, { force: true });
  const data = health?.data;
  const healthStatus = health?.status ?? "loading";
  const sectionError = (section: "nodes" | "pods") => (data ? data[section].error : undefined);
  /** A stat card: its section's own failure, else the health read's state (old numbers stay through a failed re-read). */
  const statState = (section: "nodes" | "pods") =>
    sectionError(section)
      ? { status: "error" as const, error: sectionError(section), hasData: false }
      : { status: healthStatus, error: health?.error, hasData: !!data };

  const attentionErrors = data ? ATTENTION_SECTIONS.filter((s) => data.errors[s]) : [];
  const attentionMessage = attentionErrors.map((s) => `${s}: ${data?.errors[s]}`).join("\n");
  const openAttention = (a: K8sAttentionItem) =>
    openTab(assetId, { id: resourceTabId(typeKeyOf(a), a.namespace, a.name), label: a.name });

  const events5 = (events?.data ?? []).slice(0, RECENT_EVENTS);
  const loadNodes = () => void loadList(assetId, NODES, "", { force: true });

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-4">
      <div className="grid grid-cols-3 gap-3">
        <Stat testId="k8s-stat-version" label="Kubernetes" value={version.version}>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{version.platform}</p>
        </Stat>
        <Stat testId="k8s-stat-nodes" label={t("asset.k8sNodes")}>
          <K8sAsyncState {...statState("nodes")} onRetry={retryHealth} className="py-2">
            {data && (
              <>
                <p className="mt-1 font-mono text-lg font-semibold">
                  <Num id="k8s-stat-nodes-total" value={data.nodes.total} />
                </p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">
                  <Num id="k8s-stat-nodes-schedulable" value={data.nodes.schedulable} /> {t("asset.k8sSchedulable")} ·{" "}
                  <Num id="k8s-stat-nodes-unschedulable" value={data.nodes.unschedulable} tone="text-warning" />{" "}
                  {t("asset.k8sUnschedulable")} ·{" "}
                  <Num id="k8s-stat-nodes-notready" value={data.nodes.notReady} tone="text-destructive" />{" "}
                  {t("asset.k8sNotReady")}
                </p>
              </>
            )}
          </K8sAsyncState>
        </Stat>
        <Stat testId="k8s-stat-pods" label="Pod">
          <K8sAsyncState {...statState("pods")} onRetry={retryHealth} className="py-2">
            {data && (
              <>
                <p className="mt-1 font-mono text-lg font-semibold">
                  <Num id="k8s-stat-pods-total" value={data.pods.total} />
                </p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">
                  <Num id="k8s-stat-pods-running" value={data.pods.running} /> {t("asset.k8sPodRunning")} ·{" "}
                  <Num id="k8s-stat-pods-pending" value={data.pods.pending} tone="text-warning" />{" "}
                  {t("asset.k8sPodPending")} ·{" "}
                  <Num id="k8s-stat-pods-abnormal" value={data.pods.abnormal} tone="text-destructive" />{" "}
                  {t("asset.k8sPodAbnormal")}
                </p>
              </>
            )}
          </K8sAsyncState>
        </Stat>
      </div>

      <div data-testid="k8s-attention">
        <K8sSectionCard title={t("asset.k8sAttention")} icon={AlertTriangle}>
          <K8sAsyncState
            status={healthStatus}
            error={health?.error}
            hasData={!!data}
            onRetry={retryHealth}
            className="py-4"
          >
            {data && (
              <AsyncState
                status={data.attention.length === 0 && attentionErrors.length > 0 ? "error" : "ready"}
                error={attentionMessage}
                onRetry={retryHealth}
                empty={
                  data.attention.length === 0 && attentionErrors.length === 0 ? t("asset.k8sAllHealthy") : undefined
                }
                className="py-4"
              >
                <div className="divide-y">
                  {data.attention.map((a) => (
                    <button
                      key={`${a.resource}/${a.namespace}/${a.name}`}
                      data-testid="k8s-attention-row"
                      className="flex w-full items-center gap-2 py-2 text-left text-xs hover:bg-muted/30"
                      onClick={() => openAttention(a)}
                    >
                      <span className="w-24 shrink-0 text-muted-foreground">{a.kind}</span>
                      <span className="font-mono font-medium">
                        {a.namespace ? `${a.namespace}/` : ""}
                        {a.name}
                      </span>
                      <span
                        className={`rounded-full px-1.5 py-0.5 ${statusVariantToClass(getK8sStatusColor(a.status) === "error" ? "error" : "warning")}`}
                      >
                        {a.status}
                      </span>
                      <span className="ml-auto select-text break-all text-muted-foreground">{a.reason}</span>
                      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    </button>
                  ))}
                </div>
                {attentionErrors.length > 0 && (
                  <div className="mt-2 flex items-start gap-1 whitespace-pre-wrap break-words text-xs text-warning">
                    <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span className="min-w-0 flex-1">
                      {t("asset.k8sAttentionPartial")}
                      {"\n"}
                      {attentionMessage}
                    </span>
                    <Button variant="outline" size="sm" className="h-6 shrink-0 px-2 text-xs" onClick={retryHealth}>
                      <RotateCw className="size-3" />
                      {t("action.retry")}
                    </Button>
                  </div>
                )}
              </AsyncState>
            )}
          </K8sAsyncState>
        </K8sSectionCard>
      </div>

      <div data-testid="k8s-recent-events">
        <K8sSectionCard title={t("asset.k8sRecentWarnings")} icon={Clock}>
          <K8sAsyncState
            status={events?.status ?? "loading"}
            hasData={!!events?.data}
            error={events?.error}
            onRetry={() => void loadEvents(assetId, namespace, true, { force: true })}
            empty={events?.data && events.data.length === 0 ? t("asset.k8sNoWarningEvents") : undefined}
            className="py-4"
          >
            <K8sEventsTable assetId={assetId} types={cluster.types.data?.types ?? []} events={events5} compact />
            <button
              className="mt-2 text-xs text-primary hover:underline"
              onClick={() => openTab(assetId, { id: listTabId(EVENTS), label: t("asset.k8sEvents") })}
            >
              {t("asset.k8sViewAllEvents")}
            </button>
          </K8sAsyncState>
        </K8sSectionCard>
      </div>

      <div data-testid="k8s-node-cards">
        <K8sSectionCard title={t("asset.k8sNodes")} icon={Server}>
          <K8sAsyncState
            status={nodes?.status ?? "loading"}
            hasData={!!nodes?.columns.length}
            error={nodes?.error}
            onRetry={loadNodes}
            empty={nodes?.status === "ready" && nodes.rows.length === 0 ? t("asset.k8sNoNodes") : undefined}
            className="py-4"
          >
            <div className="grid gap-3 sm:grid-cols-2">
              {nodes?.rows.map((row) => {
                const status = cellOf(nodes.columns, row, "status");
                return (
                  <button
                    key={row.name}
                    data-testid="k8s-node-card"
                    className="rounded-lg border p-3 text-left hover:bg-muted/30"
                    onClick={() => openTab(assetId, { id: resourceTabId(NODES, "", row.name), label: row.name })}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-mono text-sm font-medium">{row.name}</span>
                      <span
                        className={`rounded-full px-1.5 py-0.5 text-xs ${statusVariantToClass(getK8sStatusColor(status))}`}
                      >
                        {status}
                      </span>
                    </div>
                    <p className="mt-2 text-xs text-muted-foreground">
                      {cellOf(nodes.columns, row, "roles")} · {cellOf(nodes.columns, row, "version")}
                    </p>
                  </button>
                );
              })}
            </div>
          </K8sAsyncState>
        </K8sSectionCard>
      </div>
    </div>
  );
}
