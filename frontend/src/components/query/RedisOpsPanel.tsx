import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  AlertCircle,
  BarChart3,
  Gauge,
  Info,
  Loader2,
  Monitor,
  Network,
  RefreshCw,
  Search,
  Server,
  type LucideIcon,
} from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Input,
  Switch,
} from "@opskat/ui";
import { cn } from "@/lib/utils";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { ExecuteRedis } from "../../../wailsjs/go/query/Query";
import { RedisClusterOverview, RedisSentinelOverview } from "../../../wailsjs/go/redis/Redis";
import { redis_svc } from "../../../wailsjs/go/models";

const CLUSTER_TOTAL_SLOTS = 16384;

interface RedisInfoRow {
  section: string;
  key: string;
  value: string;
}

interface RedisKeyspaceRow {
  db: string;
  keys: number;
  expires: number;
  avgTtl: number;
}

interface RedisInfoDetails {
  values: Record<string, string>;
  rows: RedisInfoRow[];
  keyspace: RedisKeyspaceRow[];
}

interface RedisOpsPanelProps {
  tabId: string;
}

function unwrapRedisInfoResult(raw: string): string {
  try {
    const parsed = JSON.parse(raw) as { value?: unknown };
    return String(parsed.value ?? "");
  } catch {
    return raw;
  }
}

function parseKeyspaceValue(db: string, value: string): RedisKeyspaceRow {
  const item: RedisKeyspaceRow = { db, keys: 0, expires: 0, avgTtl: 0 };
  for (const part of value.split(",")) {
    const [key, raw] = part.split("=");
    const count = Number(raw || 0);
    if (!Number.isFinite(count)) continue;
    if (key === "keys") item.keys = count;
    if (key === "expires") item.expires = count;
    if (key === "avg_ttl") item.avgTtl = count;
  }
  return item;
}

function parseRedisInfoResult(raw: string): RedisInfoDetails {
  const text = unwrapRedisInfoResult(raw);
  const values: Record<string, string> = {};
  const rows: RedisInfoRow[] = [];
  const keyspace: RedisKeyspaceRow[] = [];
  let section = "";

  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    if (trimmed.startsWith("#")) {
      section = trimmed.replace(/^#+\s*/, "");
      continue;
    }

    const index = trimmed.indexOf(":");
    if (index <= 0) continue;
    const key = trimmed.slice(0, index);
    const value = trimmed.slice(index + 1);
    values[key] = value;
    rows.push({ section, key, value });

    if (/^db\d+$/.test(key)) {
      keyspace.push(parseKeyspaceValue(key, value));
    }
  }

  return { values, rows, keyspace };
}

function formatNumber(value: number | string | undefined): string {
  const number = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(number)) return value ? String(value) : "-";
  return new Intl.NumberFormat().format(number);
}

function pickValue(values: Record<string, string>, key: string, fallback = "-"): string {
  const value = values[key];
  return value === undefined || value === "" ? fallback : value;
}

function InfoPanel({
  title,
  icon: Icon,
  rows,
}: {
  title: string;
  icon: LucideIcon;
  rows: Array<{ label: string; value: string }>;
}) {
  return (
    <section className="min-w-0 rounded-md border bg-background shadow-sm">
      <div className="flex h-11 items-center gap-2 border-b px-4 text-sm font-medium">
        <Icon className="size-4 text-muted-foreground" />
        <span className="truncate">{title}</span>
      </div>
      <div className="space-y-3 p-4">
        {rows.map((row) => (
          <div key={row.label} className="min-w-0 rounded-md border bg-muted/30 px-3 py-2 text-xs">
            <span className="text-muted-foreground">{row.label}:</span>
            <span className="ml-1 break-all font-mono text-success">{row.value}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

function Stat({
  testId,
  label,
  value,
  tone,
}: {
  testId: string;
  label: string;
  value: React.ReactNode;
  tone?: "good" | "bad";
}) {
  return (
    <div data-testid={testId} className="rounded-md border bg-background px-4 py-3 shadow-sm">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-1 truncate font-mono text-base font-semibold",
          tone === "good" && "text-success",
          tone === "bad" && "text-destructive"
        )}
      >
        {value}
      </div>
    </div>
  );
}

function ClusterNodeRow({
  node,
  indent,
  roleLabel,
}: {
  node: redis_svc.RedisClusterNode;
  indent?: boolean;
  roleLabel: string;
}) {
  const failed = node.status !== "ok";
  const statusLabel = failed ? `${node.status} · ${node.error ?? ""}` : node.linkState;
  return (
    <tr
      data-testid="redis-cluster-node-row"
      data-addr={node.addr}
      data-role={node.role}
      data-status={node.status}
      className={cn(failed && "bg-destructive/5")}
    >
      <td className="border-b px-2 py-2 font-mono">
        <span className={cn("inline-flex items-center gap-1.5", indent && "pl-5 text-muted-foreground")}>
          <span className={cn("size-1.5 shrink-0 rounded-full", failed ? "bg-destructive" : "bg-success")} />
          {node.addr}
          <span className="text-[10px] text-muted-foreground">{node.id.slice(0, 8)}</span>
        </span>
      </td>
      <td className="border-b px-2 py-2">
        <span
          className={cn(
            "rounded px-1.5 py-0.5 text-[11px]",
            node.role === "master" ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground"
          )}
        >
          {roleLabel}
        </span>
      </td>
      <td className="border-b px-2 py-2 font-mono">{node.slots || ""}</td>
      <td className="border-b px-2 py-2 font-mono">{node.reachable ? formatNumber(node.keys) : "—"}</td>
      <td className="border-b px-2 py-2 font-mono">{node.reachable ? node.usedMemoryHuman : "—"}</td>
      <td className="border-b px-2 py-2 font-mono">{node.reachable ? formatNumber(node.opsPerSec) : "—"}</td>
      <td className={cn("border-b px-2 py-2", failed ? "text-destructive" : "text-muted-foreground")}>{statusLabel}</td>
    </tr>
  );
}

export function RedisOpsPanel({ tabId }: RedisOpsPanelProps) {
  const { t } = useTranslation();
  const tab = useTabStore((s) => s.tabs.find((tb) => tb.id === tabId));
  const currentDb = useQueryStore((s) => s.redisStates[tabId]?.currentDb ?? 0);
  const tabMeta = tab?.meta as QueryTabMeta | undefined;
  const isCluster = tabMeta?.redisMode === "cluster";
  const isSentinel = tabMeta?.redisMode === "sentinel";
  const [info, setInfo] = useState<RedisInfoDetails>({ values: {}, rows: [], keyspace: [] });
  const [clusterOverview, setClusterOverview] = useState<redis_svc.RedisClusterOverview | null>(null);
  const [sentinelOverview, setSentinelOverview] = useState<redis_svc.RedisSentinelOverview | null>(null);
  const [infoNode, setInfoNode] = useState("");
  const [search, setSearch] = useState("");
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 无同步 setState 的加载体:effect 直接用它;事件路径(按钮/定时器)经 refresh 先同步进入加载态。
  const fetchInfo = useCallback(async () => {
    if (!tabMeta) return;
    try {
      if (isCluster) {
        const overview = await RedisClusterOverview(tabMeta.assetId, infoNode);
        setClusterOverview(overview);
        setInfo(parseRedisInfoResult(overview.info || ""));
      } else if (isSentinel) {
        const [infoResult, overview] = await Promise.all([
          ExecuteRedis(tabMeta.assetId, "INFO", String(currentDb)),
          RedisSentinelOverview(tabMeta.assetId),
        ]);
        setInfo(parseRedisInfoResult(infoResult || ""));
        setSentinelOverview(overview);
      } else {
        const infoResult = await ExecuteRedis(tabMeta.assetId, "INFO", String(currentDb));
        setInfo(parseRedisInfoResult(infoResult || ""));
      }
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, [currentDb, tabMeta, isCluster, isSentinel, infoNode]);

  const refresh = useCallback(() => {
    if (!tabMeta) return;
    setLoading(true);
    setError(null);
    return fetchInfo();
  }, [tabMeta, fetchInfo]);

  // 挂载及刷新依据变化触发自动拉取时,同步进入加载态并清空错误:渲染期对比上次值,
  // 替代 effect 里的同步 setState。集群模式下 INFO 节点的选择才是刷新依据,数据库切换与它无关。
  const refreshDep = isCluster ? infoNode : currentDb;
  const [prevRefreshKey, setPrevRefreshKey] = useState<{ tabMeta?: QueryTabMeta; dep: string | number } | null>(null);
  if (prevRefreshKey === null || tabMeta !== prevRefreshKey.tabMeta || refreshDep !== prevRefreshKey.dep) {
    setPrevRefreshKey({ tabMeta, dep: refreshDep });
    if (tabMeta) {
      setLoading(true);
      setError(null);
    }
  }

  useEffect(() => {
    // fetchInfo 的 setState 全部在 await 之后(异步续体),用 async 包装让规则识别。
    void (async () => {
      await fetchInfo();
    })();
  }, [fetchInfo]);

  useEffect(() => {
    if (!autoRefresh) return;
    const timer = window.setInterval(refresh, 2_000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, refresh]);

  const filteredRows = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    if (!keyword) return info.rows;
    return info.rows.filter((row) => {
      return (
        row.key.toLowerCase().includes(keyword) ||
        row.value.toLowerCase().includes(keyword) ||
        row.section.toLowerCase().includes(keyword)
      );
    });
  }, [info.rows, search]);

  const values = info.values;
  const serverRows = [
    { label: t("query.redisVersion"), value: pickValue(values, "redis_version") },
    { label: t("query.redisOs"), value: pickValue(values, "os") },
    { label: t("query.redisProcessId"), value: pickValue(values, "process_id") },
  ];
  const memoryRows = [
    { label: t("query.redisMemoryUsed"), value: pickValue(values, "used_memory_human") },
    { label: t("query.redisMemoryPeak"), value: pickValue(values, "used_memory_peak_human") },
    {
      label: t("query.redisLuaMemory"),
      value: pickValue(values, "used_memory_lua_human", pickValue(values, "used_memory_lua")),
    },
  ];
  const statusRows = [
    { label: t("query.redisConnectedClients"), value: pickValue(values, "connected_clients") },
    { label: t("query.redisTotalConnections"), value: formatNumber(pickValue(values, "total_connections_received")) },
    { label: t("query.redisTotalCommands"), value: formatNumber(pickValue(values, "total_commands_processed")) },
  ];

  const clusterMasters = clusterOverview?.masters ?? [];
  const selectedInfoNode = infoNode || clusterOverview?.infoNode || "";
  const clusterReplicaCount = clusterMasters.reduce((sum, m) => sum + (m.replicas?.length ?? 0), 0);
  const clusterUnreachableCount = clusterMasters.reduce((sum, m) => {
    const masterUnreachable = m.reachable ? 0 : 1;
    const replicaUnreachable = (m.replicas ?? []).filter((r) => !r.reachable).length;
    return sum + masterUnreachable + replicaUnreachable;
  }, 0);

  const infoSourceLabel = isCluster
    ? selectedInfoNode || undefined
    : isSentinel
      ? t("query.redisSentinelCurrentMaster")
      : undefined;
  const serverTitle = infoSourceLabel ? `${t("query.redisServer")} · ${infoSourceLabel}` : t("query.redisServer");
  const fullInfoTitle = infoSourceLabel ? `${t("query.redisInfoFull")} · ${infoSourceLabel}` : t("query.redisInfoFull");

  return (
    <div className="flex h-full flex-col overflow-auto bg-background">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        {isCluster && clusterOverview && clusterOverview.state !== "ok" && (
          <span
            data-testid="redis-cluster-state-banner"
            data-unavailable-slots={CLUSTER_TOTAL_SLOTS - clusterOverview.slotsOk}
            className="flex min-w-0 items-center gap-1 truncate text-xs text-destructive"
          >
            <AlertCircle className="size-3 shrink-0" />
            <span className="truncate">
              {t("query.redisClusterStateFail", { count: CLUSTER_TOTAL_SLOTS - clusterOverview.slotsOk })}
            </span>
          </span>
        )}
        {error && (
          <span className="flex min-w-0 items-center gap-1 truncate text-xs text-destructive">
            <AlertCircle className="size-3 shrink-0" />
            <span className="truncate">{error}</span>
          </span>
        )}
        <div className="ml-auto flex items-center gap-2">
          {isCluster && (
            <>
              <span className="text-xs text-muted-foreground">{t("query.redisInfoNode")}</span>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7 gap-1.5 font-mono text-xs"
                    data-testid="redis-info-node-select"
                  >
                    {selectedInfoNode}
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" data-testid="redis-info-node-menu">
                  {clusterMasters.map((m) => (
                    <DropdownMenuItem
                      key={m.addr}
                      data-testid={`redis-info-node-option-${m.addr}`}
                      className="font-mono text-xs"
                      onSelect={() => setInfoNode(m.addr)}
                    >
                      {m.addr}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          )}
          <Button variant="outline" size="sm" className="h-7 gap-1.5 text-xs" onClick={refresh} disabled={loading}>
            {loading ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
            {t("query.refreshTree")}
          </Button>
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <span>{t("query.redisAutoRefresh")}</span>
            <Switch checked={autoRefresh} onCheckedChange={setAutoRefresh} />
          </label>
        </div>
      </div>

      <div className="space-y-4 p-3">
        {isCluster && clusterOverview && (
          <>
            <div data-testid="redis-cluster-summary" className="grid gap-3 xl:grid-cols-4">
              <Stat
                testId="redis-cluster-summary-state"
                label={t("query.redisClusterState")}
                value={clusterOverview.state}
                tone={clusterOverview.state === "ok" ? "good" : "bad"}
              />
              <Stat
                testId="redis-cluster-summary-topology"
                label={t("query.redisClusterMasterReplica")}
                value={
                  <>
                    {clusterMasters.length} / {clusterReplicaCount}
                    {clusterUnreachableCount > 0 && (
                      <span className="text-warning">
                        {t("query.redisClusterUnreachableSuffix", { count: clusterUnreachableCount })}
                      </span>
                    )}
                  </>
                }
              />
              <Stat
                testId="redis-cluster-summary-slots"
                label={t("query.redisClusterSlotCoverage")}
                value={`${clusterOverview.slotsOk} / ${CLUSTER_TOTAL_SLOTS}`}
                tone={clusterOverview.slotsOk < CLUSTER_TOTAL_SLOTS ? "bad" : undefined}
              />
              <Stat
                testId="redis-cluster-summary-keys"
                label={t("query.redisClusterKeyTotal")}
                value={
                  clusterOverview.keysPartial
                    ? `≥ ${formatNumber(clusterOverview.totalKeys)}`
                    : formatNumber(clusterOverview.totalKeys)
                }
              />
            </div>

            <section className="rounded-md border bg-background shadow-sm">
              <div className="flex h-11 items-center gap-2 border-b px-4 text-sm font-medium">
                <Network className="size-4 text-muted-foreground" />
                <span>{t("query.redisClusterNodesTitle")}</span>
                <span className="ml-auto text-xs font-normal text-muted-foreground">
                  {t("query.redisClusterNodesSource")}
                </span>
              </div>
              <div className="overflow-auto p-4">
                <table className="w-full min-w-[720px] border-separate border-spacing-0 text-xs">
                  <thead>
                    <tr className="text-left text-muted-foreground">
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColNode")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColRole")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColSlot")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColKey")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColMemory")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColOps")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColStatus")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {clusterMasters.map((master) => (
                      <Fragment key={master.addr}>
                        <ClusterNodeRow node={master} roleLabel={t("query.redisRoleMaster")} />
                        {(master.replicas ?? []).map((replica) => (
                          <ClusterNodeRow
                            key={replica.addr}
                            node={replica}
                            indent
                            roleLabel={t("query.redisRoleReplica")}
                          />
                        ))}
                      </Fragment>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          </>
        )}

        {isSentinel && sentinelOverview && (
          <>
            <div data-testid="redis-sentinel-summary" className="grid gap-3 xl:grid-cols-4">
              <Stat
                testId="redis-sentinel-summary-group"
                label={t("query.redisSentinelGroupLabel")}
                value={sentinelOverview.masterName}
              />
              <Stat
                testId="redis-sentinel-summary-master"
                label={t("query.redisSentinelCurrentMaster")}
                value={sentinelOverview.master.addr}
                tone={sentinelOverview.masterError ? "bad" : "good"}
              />
              <Stat
                testId="redis-sentinel-summary-replicas"
                label={t("query.redisSentinelReplicaSentinelCount")}
                value={`${sentinelOverview.replicas.length} / ${sentinelOverview.sentinels.length}`}
              />
              <Stat
                testId="redis-sentinel-summary-quorum"
                label={t("query.redisSentinelQuorum")}
                value={sentinelOverview.quorum}
              />
            </div>

            <section className="rounded-md border bg-background shadow-sm">
              <div className="flex h-11 items-center gap-2 border-b px-4 text-sm font-medium">
                <Network className="size-4 text-muted-foreground" />
                <span>{t("query.redisSentinelTopologyTitle")}</span>
                <span className="ml-auto text-xs font-normal text-muted-foreground">
                  {t("query.redisSentinelTopologySource")}
                </span>
              </div>
              <div className="overflow-auto p-4">
                <table className="w-full min-w-[520px] border-separate border-spacing-0 text-xs">
                  <thead>
                    <tr className="text-left text-muted-foreground">
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColNode")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColRole")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColLag")}</th>
                      <th className="border-b px-2 py-2 font-medium">{t("query.redisColStatus")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr data-testid="redis-sentinel-topology-row" data-addr={sentinelOverview.master.addr}>
                      <td className="border-b px-2 py-2 font-mono">
                        <span className="inline-flex items-center gap-1.5">
                          <span
                            className={cn(
                              "size-1.5 shrink-0 rounded-full",
                              sentinelOverview.master.status === "ok" ? "bg-success" : "bg-destructive"
                            )}
                          />
                          {sentinelOverview.master.addr}
                        </span>
                      </td>
                      <td className="border-b px-2 py-2">
                        <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">
                          {t("query.redisRoleMaster")}
                        </span>
                      </td>
                      <td className="border-b px-2 py-2 font-mono">—</td>
                      <td className="border-b px-2 py-2 text-muted-foreground">{sentinelOverview.master.status}</td>
                    </tr>
                    {sentinelOverview.replicas.map((replica) => (
                      <tr key={replica.addr} data-testid="redis-sentinel-topology-row" data-addr={replica.addr}>
                        <td className="border-b px-2 py-2 font-mono">
                          <span className="inline-flex items-center gap-1.5">
                            <span
                              className={cn(
                                "size-1.5 shrink-0 rounded-full",
                                replica.status === "ok" ? "bg-success" : "bg-destructive"
                              )}
                            />
                            {replica.addr}
                          </span>
                        </td>
                        <td className="border-b px-2 py-2">
                          <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                            {t("query.redisRoleReplica")}
                          </span>
                        </td>
                        <td className="border-b px-2 py-2 font-mono">
                          {replica.lagSeconds === -1 ? "—" : replica.lagSeconds}
                        </td>
                        <td className="border-b px-2 py-2 text-muted-foreground">{replica.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            <section className="rounded-md border bg-background shadow-sm">
              <div className="flex h-11 items-center gap-2 border-b px-4 text-sm font-medium">
                <Gauge className="size-4 text-muted-foreground" />
                <span>{t("query.redisSentinelNodesTitle")}</span>
              </div>
              <div className="overflow-auto p-4">
                <table className="w-full min-w-[420px] border-separate border-spacing-0 text-xs">
                  <tbody>
                    {sentinelOverview.sentinels.map((node) => (
                      <tr key={node.addr} data-testid="redis-sentinel-node-row" data-addr={node.addr}>
                        <td className="border-b px-2 py-2 font-mono">
                          <span className="inline-flex items-center gap-1.5">
                            <span
                              className={cn(
                                "size-1.5 shrink-0 rounded-full",
                                node.status === "ok" ? "bg-success" : "bg-destructive"
                              )}
                            />
                            {node.addr}
                          </span>
                        </td>
                        <td className="border-b px-2 py-2 text-muted-foreground">{node.status}</td>
                        <td className="border-b px-2 py-2 text-muted-foreground">
                          {node.queried ? t("query.redisSentinelQueriedNote") : ""}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          </>
        )}

        <div className="grid gap-3 xl:grid-cols-3">
          <InfoPanel title={serverTitle} icon={Server} rows={serverRows} />
          <InfoPanel title={t("query.redisMemoryPanel")} icon={Gauge} rows={memoryRows} />
          <InfoPanel title={t("query.redisRuntimeStatus")} icon={Monitor} rows={statusRows} />
        </div>

        {!isCluster && (
          <section className="rounded-md border bg-background shadow-sm">
            <div className="flex h-11 items-center gap-2 border-b px-4 text-sm font-medium">
              <BarChart3 className="size-4 text-muted-foreground" />
              <span>{t("query.redisKeyStats")}</span>
            </div>
            <div className="overflow-auto p-4">
              <table className="w-full min-w-[520px] border-separate border-spacing-0 text-xs">
                <thead>
                  <tr className="text-left text-muted-foreground">
                    <th className="border-b px-2 py-2 font-medium">{t("query.redisDb")}</th>
                    <th className="border-b px-2 py-2 font-medium">{t("query.redisKeys")}</th>
                    <th className="border-b px-2 py-2 font-medium">{t("query.redisExpires")}</th>
                    <th className="border-b px-2 py-2 font-medium">{t("query.redisAvgTtl")}</th>
                  </tr>
                </thead>
                <tbody>
                  {info.keyspace.map((row) => (
                    <tr key={row.db}>
                      <td className="border-b px-2 py-2 font-mono text-muted-foreground">{row.db}</td>
                      <td className="border-b px-2 py-2 font-mono">{formatNumber(row.keys)}</td>
                      <td className="border-b px-2 py-2 font-mono">{formatNumber(row.expires)}</td>
                      <td className="border-b px-2 py-2 font-mono">{formatNumber(row.avgTtl)}</td>
                    </tr>
                  ))}
                  {!loading && info.keyspace.length === 0 && (
                    <tr>
                      <td className="px-2 py-6 text-center text-muted-foreground" colSpan={4}>
                        {t("query.redisOpsEmpty")}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </section>
        )}

        <section className="rounded-md border bg-background shadow-sm">
          <div className="flex h-11 items-center gap-2 border-b px-4 text-sm font-medium">
            <Info className="size-4 text-muted-foreground" />
            <span>{fullInfoTitle}</span>
            <div className="relative ml-auto w-64 max-w-[45%]">
              <Search className="absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
              <Input
                className="h-7 pl-7 text-xs"
                placeholder={t("query.redisInfoSearch")}
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </div>
          </div>
          <div className="overflow-auto p-4">
            <table className="w-full min-w-[620px] border-separate border-spacing-0 text-xs">
              <thead>
                <tr className="text-left text-muted-foreground">
                  <th className="border-b px-2 py-2 font-medium">{t("query.redisInfoKey")}</th>
                  <th className="border-b px-2 py-2 font-medium">{t("query.redisInfoValue")}</th>
                </tr>
              </thead>
              <tbody>
                {filteredRows.map((row) => (
                  <tr key={`${row.section}-${row.key}`} className="odd:bg-muted/20">
                    <td className="border-b px-2 py-2 font-mono text-muted-foreground">{row.key}</td>
                    <td className="border-b px-2 py-2 font-mono break-all">{row.value}</td>
                  </tr>
                ))}
                {!loading && filteredRows.length === 0 && (
                  <tr>
                    <td className="px-2 py-6 text-center text-muted-foreground" colSpan={2}>
                      {t("query.redisOpsEmpty")}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  );
}
