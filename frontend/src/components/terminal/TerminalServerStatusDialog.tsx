import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Activity, BarChart3, Cpu, HardDrive, Loader2, RefreshCw, Server } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Switch } from "@opskat/ui";
import { GetSSHServerStatus } from "../../../wailsjs/go/ssh/SSH";

interface TerminalServerStatusDialogProps {
  open: boolean;
  sessionId: string;
  onOpenChange: (open: boolean) => void;
}

interface ServerStatusSnapshot {
  hostname?: string;
  os?: string;
  uptime?: string;
  cpuPercent?: number;
  load1?: number;
  load5?: number;
  load15?: number;
  memoryUsedBytes?: number;
  memoryTotalBytes?: number;
  diskMount?: string;
  diskUsedBytes?: number;
  diskTotalBytes?: number;
  collectedAt?: number;
}

function formatPercent(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return "-";
  return `${value.toFixed(1)}%`;
}

function formatBytes(value: number | undefined): string {
  if (!value || value <= 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const normalized = value / Math.pow(1024, index);
  return `${normalized.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function usagePercent(used: number | undefined, total: number | undefined): number | null {
  if (!used || !total || total <= 0) return null;
  return Math.max(0, Math.min(100, (used / total) * 100));
}

function loadBarPercent(value: number | undefined): number {
  if (!value || value <= 0) return 0;
  return Math.max(8, Math.min(100, value * 100));
}

function UsageChart({
  icon: Icon,
  label,
  percent,
  summary,
  detail,
}: {
  icon: typeof Cpu;
  label: string;
  percent: number | null;
  summary: string;
  detail: string;
}) {
  const safePercent = percent ?? 0;

  return (
    <section className="rounded-lg border bg-background p-4 shadow-sm">
      <div className="mb-4 flex items-center gap-2 text-sm font-medium">
        <Icon className="size-4 text-muted-foreground" />
        <span>{label}</span>
      </div>
      <div className="flex items-center gap-4">
        <div
          className="grid size-20 place-items-center rounded-full border text-center"
          style={{
            backgroundImage: `conic-gradient(hsl(var(--primary)) ${safePercent}%, hsl(var(--muted)) 0)`,
          }}
        >
          <div className="grid size-14 place-items-center rounded-full bg-background">
            <span className="text-xs font-semibold tabular-nums">{formatPercent(percent)}</span>
          </div>
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-lg font-semibold tabular-nums">{summary}</div>
          <div className="mt-1 text-xs text-muted-foreground">{detail}</div>
          <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${safePercent}%` }} />
          </div>
        </div>
      </div>
    </section>
  );
}

function LoadChart({ snapshot }: { snapshot: ServerStatusSnapshot | null }) {
  const { t } = useTranslation();
  const loads = [
    { label: t("terminal.serverStatus.load1"), value: snapshot?.load1 ?? 0 },
    { label: t("terminal.serverStatus.load5"), value: snapshot?.load5 ?? 0 },
    { label: t("terminal.serverStatus.load15"), value: snapshot?.load15 ?? 0 },
  ];

  return (
    <section className="rounded-lg border bg-background p-4 shadow-sm">
      <div className="mb-4 flex items-center gap-2 text-sm font-medium">
        <BarChart3 className="size-4 text-muted-foreground" />
        <span>{t("terminal.serverStatus.loadAverage")}</span>
      </div>
      <div className="grid grid-cols-3 gap-3">
        {loads.map((item) => (
          <div key={item.label} className="rounded-md border bg-muted/20 p-3">
            <div className="mb-2 text-xs text-muted-foreground">{item.label}</div>
            <div className="mb-3 text-lg font-semibold tabular-nums">{item.value ? item.value.toFixed(2) : "-"}</div>
            <div className="flex h-28 items-end">
              <div
                className="w-full rounded-t-md bg-primary/85 transition-all"
                style={{ height: `${loadBarPercent(item.value)}%` }}
              />
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-1 break-all text-sm font-medium">{value || "-"}</div>
    </div>
  );
}

export function TerminalServerStatusDialog({ open, sessionId, onOpenChange }: TerminalServerStatusDialogProps) {
  const { t } = useTranslation();
  const [snapshot, setSnapshot] = useState<ServerStatusSnapshot | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(false);

  const refresh = useCallback(async () => {
    if (!sessionId) return;
    setLoading(true);
    setError(null);
    try {
      const result = (await GetSSHServerStatus(sessionId)) as ServerStatusSnapshot;
      setSnapshot(result || null);
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, [sessionId]);

  useEffect(() => {
    if (!open) return;
    refresh();
  }, [open, refresh]);

  useEffect(() => {
    if (!open || !autoRefresh) return;
    const timer = window.setInterval(refresh, 5_000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, open, refresh]);

  const memoryPercent = useMemo(
    () => usagePercent(snapshot?.memoryUsedBytes, snapshot?.memoryTotalBytes),
    [snapshot?.memoryTotalBytes, snapshot?.memoryUsedBytes]
  );
  const diskPercent = useMemo(
    () => usagePercent(snapshot?.diskUsedBytes, snapshot?.diskTotalBytes),
    [snapshot?.diskTotalBytes, snapshot?.diskUsedBytes]
  );
  const collectedAtText = snapshot?.collectedAt ? new Date(snapshot.collectedAt).toLocaleTimeString() : "-";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] max-w-5xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("terminal.serverStatus.title")}</DialogTitle>
          <DialogDescription>{t("terminal.serverStatus.description")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs font-medium">
              <Server className="size-3.5 text-muted-foreground" />
              {snapshot?.hostname || t("terminal.serverStatus.notAvailable")}
            </span>
            <span className="inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs font-medium text-muted-foreground">
              {snapshot?.os || t("terminal.serverStatus.notAvailable")}
            </span>
            <span className="text-xs text-muted-foreground">
              {t("terminal.serverStatus.lastUpdated")}: {collectedAtText}
            </span>
          </div>
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <Switch checked={autoRefresh} onCheckedChange={setAutoRefresh} />
              <span>{t("terminal.serverStatus.autoRefresh")}</span>
            </label>
            <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
              {loading ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : <RefreshCw className="mr-1.5 size-4" />}
              {t("terminal.serverStatus.refresh")}
            </Button>
          </div>
        </div>

        {error && (
          <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {t("terminal.serverStatus.error")}: {error}
          </div>
        )}

        <div className="grid gap-4 lg:grid-cols-3">
          <UsageChart
            icon={Cpu}
            label={t("terminal.serverStatus.cpu")}
            percent={typeof snapshot?.cpuPercent === "number" ? snapshot.cpuPercent : null}
            summary={formatPercent(typeof snapshot?.cpuPercent === "number" ? snapshot.cpuPercent : null)}
            detail={t("terminal.serverStatus.cpuDetail")}
          />
          <UsageChart
            icon={Activity}
            label={t("terminal.serverStatus.memory")}
            percent={memoryPercent}
            summary={`${formatBytes(snapshot?.memoryUsedBytes)} / ${formatBytes(snapshot?.memoryTotalBytes)}`}
            detail={t("terminal.serverStatus.memoryDetail")}
          />
          <UsageChart
            icon={HardDrive}
            label={t("terminal.serverStatus.disk")}
            percent={diskPercent}
            summary={`${formatBytes(snapshot?.diskUsedBytes)} / ${formatBytes(snapshot?.diskTotalBytes)}`}
            detail={`${t("terminal.serverStatus.diskDetail")} ${snapshot?.diskMount || "/"}`}
          />
        </div>

        <div className="grid gap-4 lg:grid-cols-[1.2fr_0.8fr]">
          <section className="rounded-lg border bg-background p-4 shadow-sm">
            <div className="mb-4 flex items-center gap-2 text-sm font-medium">
              <Server className="size-4 text-muted-foreground" />
              <span>{t("terminal.serverStatus.overview")}</span>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <InfoRow label={t("terminal.serverStatus.host")} value={snapshot?.hostname || "-"} />
              <InfoRow label={t("terminal.serverStatus.os")} value={snapshot?.os || "-"} />
              <InfoRow label={t("terminal.serverStatus.uptime")} value={snapshot?.uptime || "-"} />
              <InfoRow
                label={t("terminal.serverStatus.memoryUsage")}
                value={`${formatBytes(snapshot?.memoryUsedBytes)} / ${formatBytes(snapshot?.memoryTotalBytes)}`}
              />
              <InfoRow
                label={t("terminal.serverStatus.diskUsage")}
                value={`${formatBytes(snapshot?.diskUsedBytes)} / ${formatBytes(snapshot?.diskTotalBytes)}`}
              />
              <InfoRow label={t("terminal.serverStatus.diskMount")} value={snapshot?.diskMount || "/"} />
            </div>
          </section>

          <LoadChart snapshot={snapshot} />
        </div>
      </DialogContent>
    </Dialog>
  );
}
