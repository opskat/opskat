import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Activity, BarChart3, ChevronDown, ChevronUp, Cpu, HardDrive, Loader2, RefreshCw, Server } from "lucide-react";
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

type HealthLevel = "healthy" | "warning" | "critical" | "unknown";

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

function formatLoad(value: number | undefined): string {
  if (!value || value <= 0) return "-";
  return value.toFixed(2);
}

function usagePercent(used: number | undefined, total: number | undefined): number | null {
  if (!used || !total || total <= 0) return null;
  return Math.max(0, Math.min(100, (used / total) * 100));
}

function getUsageTone(percent: number | null) {
  if (percent === null) {
    return {
      badgeClass: "bg-muted text-muted-foreground",
      barClass: "bg-muted-foreground/35",
      textClass: "text-foreground",
    };
  }
  if (percent >= 85) {
    return {
      badgeClass: "bg-red-500/12 text-red-600 dark:text-red-400",
      barClass: "bg-red-500",
      textClass: "text-red-600 dark:text-red-400",
    };
  }
  if (percent >= 60) {
    return {
      badgeClass: "bg-amber-500/12 text-amber-600 dark:text-amber-400",
      barClass: "bg-amber-500",
      textClass: "text-amber-600 dark:text-amber-400",
    };
  }
  return {
    badgeClass: "bg-emerald-500/12 text-emerald-600 dark:text-emerald-400",
    barClass: "bg-emerald-500",
    textClass: "text-emerald-600 dark:text-emerald-400",
  };
}

function getHealthLevel(values: Array<number | null>): HealthLevel {
  const available = values.filter((value): value is number => value !== null && Number.isFinite(value));
  if (!available.length) return "unknown";
  const peak = Math.max(...available);
  if (peak >= 85) return "critical";
  if (peak >= 60) return "warning";
  return "healthy";
}

function getHealthTone(level: HealthLevel) {
  switch (level) {
    case "critical":
      return {
        badgeClass: "border-red-500/25 bg-red-500/10 text-red-600 dark:text-red-400",
        dotClass: "bg-red-500",
        titleClass: "text-red-600 dark:text-red-400",
      };
    case "warning":
      return {
        badgeClass: "border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-400",
        dotClass: "bg-amber-500",
        titleClass: "text-amber-600 dark:text-amber-400",
      };
    case "healthy":
      return {
        badgeClass: "border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
        dotClass: "bg-emerald-500",
        titleClass: "text-emerald-600 dark:text-emerald-400",
      };
    default:
      return {
        badgeClass: "border-border bg-muted/50 text-muted-foreground",
        dotClass: "bg-muted-foreground/60",
        titleClass: "text-foreground",
      };
  }
}

function getHealthLabelKey(level: HealthLevel) {
  switch (level) {
    case "critical":
      return "terminal.serverStatus.critical";
    case "warning":
      return "terminal.serverStatus.warning";
    case "healthy":
      return "terminal.serverStatus.healthy";
    default:
      return "terminal.serverStatus.unknown";
  }
}

function metricWidth(percent: number | null): string {
  if (percent === null) return "0%";
  return `${Math.max(10, Math.min(100, percent))}%`;
}

function loadWidth(value: number | undefined): string {
  if (!value || value <= 0) return "0%";
  return `${Math.max(14, Math.min(100, value * 100))}%`;
}

function MetricCard({
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
  const tone = getUsageTone(percent);

  return (
    <section className="rounded-xl border bg-background p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-3">
          <span className={`flex size-9 items-center justify-center rounded-lg ${tone.badgeClass}`}>
            <Icon className="size-4" />
          </span>
          <div>
            <div className="text-sm font-medium">{label}</div>
            <div className="mt-1 text-xs text-muted-foreground">{detail}</div>
          </div>
        </div>
        <div className={`text-right text-2xl font-semibold tabular-nums ${tone.textClass}`}>
          {formatPercent(percent)}
        </div>
      </div>
      <div className="mt-5 flex items-end justify-between gap-3">
        <div className="min-w-0 text-lg font-semibold tabular-nums">{summary}</div>
      </div>
      <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={`h-full rounded-full transition-all ${tone.barClass}`}
          style={{ width: metricWidth(percent) }}
        />
      </div>
    </section>
  );
}

function SummaryChip({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-background/80 px-3 py-2">
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-1 text-base font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function LoadSummary({ snapshot }: { snapshot: ServerStatusSnapshot | null }) {
  const { t } = useTranslation();
  const loads = [
    { label: t("terminal.serverStatus.load1"), value: snapshot?.load1 },
    { label: t("terminal.serverStatus.load5"), value: snapshot?.load5 },
    { label: t("terminal.serverStatus.load15"), value: snapshot?.load15 },
  ];

  return (
    <section className="rounded-xl border bg-background p-4">
      <div className="mb-4 flex items-center gap-2 text-sm font-medium">
        <BarChart3 className="size-4 text-muted-foreground" />
        <span>{t("terminal.serverStatus.loadAverage")}</span>
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        {loads.map((item) => (
          <div key={item.label} className="rounded-lg border bg-muted/20 p-3">
            <div className="text-xs text-muted-foreground">{item.label}</div>
            <div className="mt-2 text-2xl font-semibold tabular-nums">{formatLoad(item.value)}</div>
            <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary/80 transition-all"
                style={{ width: loadWidth(item.value) }}
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
    <div className="rounded-lg border bg-muted/20 px-3 py-2">
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
  const [detailsOpen, setDetailsOpen] = useState(true);

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

  const cpuPercent = typeof snapshot?.cpuPercent === "number" ? snapshot.cpuPercent : null;
  const memoryPercent = useMemo(
    () => usagePercent(snapshot?.memoryUsedBytes, snapshot?.memoryTotalBytes),
    [snapshot?.memoryTotalBytes, snapshot?.memoryUsedBytes]
  );
  const diskPercent = useMemo(
    () => usagePercent(snapshot?.diskUsedBytes, snapshot?.diskTotalBytes),
    [snapshot?.diskTotalBytes, snapshot?.diskUsedBytes]
  );
  const collectedAtText = snapshot?.collectedAt ? new Date(snapshot.collectedAt).toLocaleTimeString() : "-";
  const healthLevel = useMemo(
    () => getHealthLevel([cpuPercent, memoryPercent, diskPercent]),
    [cpuPercent, memoryPercent, diskPercent]
  );
  const healthTone = getHealthTone(healthLevel);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        size="xl"
        resizable
        className="h-[90vh] min-h-[720px] overflow-hidden p-0 sm:max-w-[min(96vw,96rem)]"
      >
        <div className="flex h-full flex-col overflow-hidden">
          <DialogHeader className="shrink-0 border-b px-6 py-5">
            <DialogTitle>{t("terminal.serverStatus.title")}</DialogTitle>
            <DialogDescription>{t("terminal.serverStatus.description")}</DialogDescription>
          </DialogHeader>

          <div className="flex-1 overflow-y-auto px-6 py-5">
            <div className="space-y-4">
              <section className="rounded-xl border bg-gradient-to-br from-muted/30 via-background to-background p-4">
                <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
                  <div className="space-y-4">
                    <div className="flex items-start gap-3">
                      <span
                        className={`mt-0.5 flex size-10 items-center justify-center rounded-xl border ${healthTone.badgeClass}`}
                      >
                        <Server className="size-5" />
                      </span>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <h3 className="text-lg font-semibold">
                            {snapshot?.hostname || t("terminal.serverStatus.notAvailable")}
                          </h3>
                          <span
                            className={`inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-medium ${healthTone.badgeClass}`}
                          >
                            <span className={`size-2 rounded-full ${healthTone.dotClass}`} />
                            {t(getHealthLabelKey(healthLevel))}
                          </span>
                        </div>
                        <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
                          <span className="rounded-full border px-2.5 py-1">
                            {snapshot?.os || t("terminal.serverStatus.notAvailable")}
                          </span>
                          <span className="rounded-full border px-2.5 py-1">
                            {t("terminal.serverStatus.uptime")}: {snapshot?.uptime || "-"}
                          </span>
                          <span className="rounded-full border px-2.5 py-1">
                            {t("terminal.serverStatus.lastUpdated")}: {collectedAtText}
                          </span>
                        </div>
                        <div className="mt-4 flex flex-wrap items-center gap-3">
                          <label className="flex items-center gap-2 text-xs text-muted-foreground">
                            <Switch checked={autoRefresh} onCheckedChange={setAutoRefresh} />
                            <span>{t("terminal.serverStatus.autoRefresh")}</span>
                          </label>
                          <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
                            {loading ? (
                              <Loader2 className="mr-1.5 size-4 animate-spin" />
                            ) : (
                              <RefreshCw className="mr-1.5 size-4" />
                            )}
                            {t("terminal.serverStatus.refresh")}
                          </Button>
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="grid gap-2 sm:grid-cols-3 xl:min-w-[360px]">
                    <SummaryChip
                      label={t("terminal.serverStatus.healthSummary")}
                      value={t(getHealthLabelKey(healthLevel))}
                    />
                    <SummaryChip label={t("terminal.serverStatus.cpu")} value={formatPercent(cpuPercent)} />
                    <SummaryChip label={t("terminal.serverStatus.load1")} value={formatLoad(snapshot?.load1)} />
                  </div>
                </div>
              </section>

              {error && (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {t("terminal.serverStatus.error")}: {error}
                </div>
              )}

              <section className="space-y-3">
                <div className="flex items-center gap-2 text-sm font-medium">
                  <Activity className="size-4 text-muted-foreground" />
                  <span>{t("terminal.serverStatus.resourceUsage")}</span>
                </div>
                <div className="grid gap-3 lg:grid-cols-3">
                  <MetricCard
                    icon={Cpu}
                    label={t("terminal.serverStatus.cpu")}
                    percent={cpuPercent}
                    summary={formatPercent(cpuPercent)}
                    detail={t("terminal.serverStatus.cpuDetail")}
                  />
                  <MetricCard
                    icon={Activity}
                    label={t("terminal.serverStatus.memory")}
                    percent={memoryPercent}
                    summary={`${formatBytes(snapshot?.memoryUsedBytes)} / ${formatBytes(snapshot?.memoryTotalBytes)}`}
                    detail={t("terminal.serverStatus.memoryDetail")}
                  />
                  <MetricCard
                    icon={HardDrive}
                    label={t("terminal.serverStatus.disk")}
                    percent={diskPercent}
                    summary={`${formatBytes(snapshot?.diskUsedBytes)} / ${formatBytes(snapshot?.diskTotalBytes)}`}
                    detail={`${t("terminal.serverStatus.diskDetail")} ${snapshot?.diskMount || "/"}`}
                  />
                </div>
              </section>

              <div className="grid gap-4 lg:grid-cols-[0.95fr_1.05fr]">
                <LoadSummary snapshot={snapshot} />

                <section className="rounded-xl border bg-background">
                  <button
                    type="button"
                    className="flex w-full items-center justify-between gap-3 px-4 py-4 text-left"
                    aria-expanded={detailsOpen}
                    onClick={() => setDetailsOpen((current) => !current)}
                  >
                    <div className="flex items-center gap-2 text-sm font-medium">
                      <Server className="size-4 text-muted-foreground" />
                      <span>{t("terminal.serverStatus.details")}</span>
                    </div>
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <span>
                        {detailsOpen ? t("terminal.serverStatus.hideDetails") : t("terminal.serverStatus.showDetails")}
                      </span>
                      {detailsOpen ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
                    </div>
                  </button>
                  {detailsOpen && (
                    <div className="border-t px-4 pb-4 pt-3">
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
                    </div>
                  )}
                </section>
              </div>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
