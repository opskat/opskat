import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Clock3, Database, Gauge, Loader2, Monitor, RefreshCw, Server, TerminalSquare } from "lucide-react";
import { Button } from "@opskat/ui";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { ExecuteRedis, RedisClientList, RedisCommandHistory, RedisSlowLog } from "../../../wailsjs/go/app/App";

interface RedisSlowLogEntry {
  id: number;
  timestamp: number;
  durationMicros: number;
  command: string[];
  client?: string;
  clientName?: string;
}

interface RedisCommandHistoryEntry {
  assetId: number;
  db: number;
  command: string;
  costMillis: number;
  error?: string;
  timestamp: number;
}

interface RedisInfoSummary {
  version: string;
  uptime: string;
  memory: string;
  clients: string;
  commands: string;
  keyspace: string;
}

interface RedisOpsPanelProps {
  tabId: string;
}

function parseClientLines(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .slice(0, 8);
}

function formatTime(timestamp: number): string {
  const millis = timestamp < 10_000_000_000 ? timestamp * 1000 : timestamp;
  return new Date(millis).toLocaleTimeString();
}

function formatUptime(secondsText: string | undefined): string {
  const seconds = Number(secondsText || "0");
  if (!Number.isFinite(seconds) || seconds <= 0) return "-";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function parseRedisInfoResult(raw: string): RedisInfoSummary {
  let text = raw;
  try {
    const parsed = JSON.parse(raw) as { value?: unknown };
    text = String(parsed.value ?? "");
  } catch {
    /* raw INFO text */
  }

  const values: Record<string, string> = {};
  const dbLines: string[] = [];
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const index = trimmed.indexOf(":");
    if (index <= 0) continue;
    const key = trimmed.slice(0, index);
    const value = trimmed.slice(index + 1);
    values[key] = value;
    if (/^db\d+$/.test(key)) {
      dbLines.push(value);
    }
  }

  const totalKeys = dbLines.reduce((sum, line) => {
    const match = line.match(/(?:^|,)keys=(\d+)/);
    return sum + (match ? Number(match[1]) : 0);
  }, 0);

  return {
    version: values.redis_version || "-",
    uptime: formatUptime(values.uptime_in_seconds),
    memory: values.used_memory_human || "-",
    clients: values.connected_clients || "-",
    commands: values.total_commands_processed || "-",
    keyspace: totalKeys > 0 ? `${totalKeys}` : "-",
  };
}

export function RedisOpsPanel({ tabId }: RedisOpsPanelProps) {
  const { t } = useTranslation();
  const tab = useTabStore((s) => s.tabs.find((tb) => tb.id === tabId));
  const currentDb = useQueryStore((s) => s.redisStates[tabId]?.currentDb ?? 0);
  const tabMeta = tab?.meta as QueryTabMeta | undefined;
  const [info, setInfo] = useState<RedisInfoSummary | null>(null);
  const [slowLog, setSlowLog] = useState<RedisSlowLogEntry[]>([]);
  const [clients, setClients] = useState<string[]>([]);
  const [history, setHistory] = useState<RedisCommandHistoryEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!tabMeta) return;
    setLoading(true);
    setError(null);
    try {
      const [infoResult, slow, clientList, commandHistory] = await Promise.all([
        ExecuteRedis(tabMeta.assetId, "INFO", currentDb),
        RedisSlowLog(tabMeta.assetId, 128),
        RedisClientList(tabMeta.assetId),
        RedisCommandHistory(tabMeta.assetId, 50),
      ]);
      setInfo(parseRedisInfoResult(infoResult || ""));
      setSlowLog((slow || []) as RedisSlowLogEntry[]);
      setClients(parseClientLines(clientList || ""));
      setHistory((commandHistory || []) as RedisCommandHistoryEntry[]);
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, [currentDb, tabMeta]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <div className="flex h-full flex-col overflow-hidden bg-background">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b px-3">
        <Server className="size-3.5 text-muted-foreground" />
        <span className="text-xs font-medium">{t("query.redisStatus")}</span>
        {error && (
          <span className="flex min-w-0 items-center gap-1 truncate text-xs text-destructive">
            <AlertCircle className="size-3 shrink-0" />
            <span className="truncate">{error}</span>
          </span>
        )}
        <Button variant="ghost" size="icon-xs" className="ml-auto" onClick={refresh} disabled={loading}>
          {loading ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
        </Button>
      </div>

      <div className="grid shrink-0 grid-cols-2 gap-2 border-b p-3 text-xs md:grid-cols-3 xl:grid-cols-6">
        {[
          { label: t("query.redisVersion"), value: info?.version || "-", icon: Server },
          { label: t("query.redisUptime"), value: info?.uptime || "-", icon: Clock3 },
          { label: t("query.redisMemory"), value: info?.memory || "-", icon: Gauge },
          { label: t("query.redisClients"), value: info?.clients || "-", icon: Monitor },
          { label: t("query.redisCommands"), value: info?.commands || "-", icon: TerminalSquare },
          { label: t("query.redisKeyspace"), value: info?.keyspace || "-", icon: Database },
        ].map((item) => {
          const Icon = item.icon;
          return (
            <div key={item.label} className="min-w-0 rounded-md border bg-muted/20 px-3 py-2">
              <div className="mb-1 flex items-center gap-1.5 text-muted-foreground">
                <Icon className="size-3" />
                <span className="truncate">{item.label}</span>
              </div>
              <div className="truncate font-mono text-sm font-medium">{item.value}</div>
            </div>
          );
        })}
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-3 divide-x text-xs">
        <section className="min-w-0 overflow-auto p-2">
          <div className="mb-1 flex items-center gap-1 font-medium text-muted-foreground">
            <Clock3 className="size-3" />
            {t("query.redisSlowLog")}
          </div>
          <div className="space-y-1">
            {slowLog.slice(0, 8).map((entry) => (
              <div key={entry.id} className="rounded border px-2 py-1">
                <div className="truncate font-mono">
                  #{entry.id} {entry.command.join(" ")}
                </div>
                <div className="truncate text-[11px] text-muted-foreground">
                  {formatTime(entry.timestamp)} · {entry.durationMicros}us
                  {entry.client ? ` · ${entry.client}` : ""}
                </div>
              </div>
            ))}
            {!loading && slowLog.length === 0 && (
              <div className="text-muted-foreground">{t("query.redisOpsEmpty")}</div>
            )}
          </div>
        </section>

        <section className="min-w-0 overflow-auto p-2">
          <div className="mb-1 flex items-center gap-1 font-medium text-muted-foreground">
            <Monitor className="size-3" />
            {t("query.redisClients")}
          </div>
          <div className="space-y-1 font-mono text-[11px]">
            {clients.map((client, index) => (
              <div key={`${client}-${index}`} className="truncate rounded border px-2 py-1">
                {client}
              </div>
            ))}
            {!loading && clients.length === 0 && (
              <div className="font-sans text-muted-foreground">{t("query.redisOpsEmpty")}</div>
            )}
          </div>
        </section>

        <section className="min-w-0 overflow-auto p-2">
          <div className="mb-1 flex items-center gap-1 font-medium text-muted-foreground">
            <TerminalSquare className="size-3" />
            {t("query.redisCommandHistory")}
          </div>
          <div className="space-y-1">
            {history.slice(0, 10).map((entry, index) => (
              <div key={`${entry.timestamp}-${index}`} className="rounded border px-2 py-1">
                <div className="truncate font-mono">{entry.command}</div>
                <div className="truncate text-[11px] text-muted-foreground">
                  db{entry.db} · {entry.costMillis}ms{entry.error ? ` · ${entry.error}` : ""}
                </div>
              </div>
            ))}
            {!loading && history.length === 0 && (
              <div className="text-muted-foreground">{t("query.redisOpsEmpty")}</div>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
