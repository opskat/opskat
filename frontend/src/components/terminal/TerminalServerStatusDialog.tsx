import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  Activity,
  BarChart3,
  ChevronDown,
  ChevronUp,
  Cpu,
  Gauge,
  HardDrive,
  Loader2,
  MemoryStick,
  RefreshCw,
  Server,
} from "lucide-react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} from "@opskat/ui";
import { useServerStatusStore } from "@/stores/serverStatusStore";
import {
  formatBytes,
  formatLoad,
  formatPercent,
  formatUptime,
  getHealthLevel,
  usagePercent,
  type HealthLevel,
} from "@/components/terminal/serverStatusMetrics";
import { Sparkline } from "@/components/terminal/Sparkline";

interface TerminalServerStatusDialogProps {
  open: boolean;
  sessionId: string;
  onOpenChange: (open: boolean) => void;
}

const METRIC_COLOR = {
  cpu: "var(--success)",
  memory: "var(--warning)",
  load: "var(--primary)",
};

function healthClasses(level: HealthLevel) {
  switch (level) {
    case "critical":
      return { badge: "border-destructive/30 bg-destructive/15 text-destructive", dot: "bg-destructive" };
    case "warning":
      return { badge: "border-warning/30 bg-warning/15 text-warning", dot: "bg-warning" };
    case "healthy":
      return {
        badge: "border-success/30 bg-success/15 text-success",
        dot: "bg-success",
      };
    default:
      return { badge: "border-border bg-muted/50 text-muted-foreground", dot: "bg-muted-foreground/60" };
  }
}

function healthLabelKey(level: HealthLevel) {
  return `terminal.serverStatus.${level}`;
}

export function TerminalServerStatusDialog({ open, sessionId, onOpenChange }: TerminalServerStatusDialogProps) {
  const { i18n, t } = useTranslation();
  const [detailsOpen, setDetailsOpen] = useState(true);
  const session = useServerStatusStore((s) => s.sessions[sessionId]);
  const activate = useServerStatusStore((s) => s.activate);
  const setPaused = useServerStatusStore((s) => s.setPaused);
  const setSessionInterval = useServerStatusStore((s) => s.setSessionInterval);
  const refreshNow = useServerStatusStore((s) => s.refreshNow);

  // D1: 首次打开懒启动采集；之后即使关闭也持续采集
  useEffect(() => {
    if (open) activate(sessionId);
  }, [open, sessionId, activate]);

  const buffer = session?.buffer ?? [];
  const latest = buffer.length ? buffer[buffer.length - 1] : null;
  const autoRefresh = !(session?.paused ?? false);
  const intervalMs = session?.intervalMs ?? 5000;
  const loading = session?.loading ?? false;
  const error = session?.error ?? null;

  const cpuPercent = typeof latest?.cpuPercent === "number" ? latest.cpuPercent : null;
  const memoryPercent = usagePercent(latest?.memoryUsedBytes, latest?.memoryTotalBytes);
  const diskPercent = usagePercent(latest?.diskUsedBytes, latest?.diskTotalBytes);
  const health = getHealthLevel(cpuPercent, memoryPercent, diskPercent);
  const tone = healthClasses(health);

  const cpuSeries = buffer.map((s) => (typeof s.cpuPercent === "number" ? s.cpuPercent : 0));
  const memSeries = buffer.map((s) => usagePercent(s.memoryUsedBytes, s.memoryTotalBytes) ?? 0);
  // 折线仅绘制 1 分钟负载；5/15 分钟以数值展示
  const loadSeries = buffer.map((s) => s.load1 ?? 0);
  const collectedAtText = latest?.collectedAt ? new Date(latest.collectedAt).toLocaleTimeString() : "-";
  const uptimeText = formatUptime(latest?.uptime, i18n.language);
  const hasTrend = buffer.length >= 2;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="xl" className="flex max-h-[88vh] flex-col overflow-hidden p-0 sm:max-w-4xl">
        {/* Header（固定） */}
        <DialogHeader className="shrink-0 border-b px-5 py-4">
          <DialogTitle className="flex items-center gap-2 text-[15px]">
            <Gauge className="size-[18px] text-muted-foreground" />
            {t("terminal.serverStatus.title")}
          </DialogTitle>
          <DialogDescription>{t("terminal.serverStatus.description")}</DialogDescription>
        </DialogHeader>

        {/* 身份条（固定） */}
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-b bg-background/40 px-5 py-3">
          <div className="flex min-w-0 items-center gap-3">
            <span className={`flex size-9 items-center justify-center rounded-lg border ${tone.badge}`}>
              <Server className="size-[18px]" />
            </span>
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="truncate font-semibold">
                  {latest?.hostname || t("terminal.serverStatus.notAvailable")}
                </span>
                <span
                  className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${tone.badge}`}
                >
                  <span className={`size-1.5 rounded-full ${tone.dot}`} />
                  {t(healthLabelKey(health))}
                </span>
              </div>
              <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground">
                <span className="inline-flex items-center gap-1">
                  <Cpu className="size-3" />
                  {latest?.os || t("terminal.serverStatus.notAvailable")}
                </span>
                <span className="inline-flex items-center gap-1">
                  {t("terminal.serverStatus.uptime")}: {uptimeText}
                </span>
                <span className="inline-flex items-center gap-1">
                  <span
                    className={`size-1.5 rounded-full ${autoRefresh ? "animate-pulse bg-success" : "bg-muted-foreground/60"}`}
                  />
                  {autoRefresh
                    ? `${t("terminal.serverStatus.lastUpdated")}: ${collectedAtText}`
                    : t("terminal.serverStatus.paused")}
                </span>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <Switch checked={autoRefresh} onCheckedChange={(v) => setPaused(sessionId, !v)} />
              {t("terminal.serverStatus.autoRefresh")}
            </label>
            <Select value={String(intervalMs)} onValueChange={(v) => setSessionInterval(sessionId, Number(v))}>
              <SelectTrigger className="h-8 w-[72px] text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="3000">3s</SelectItem>
                <SelectItem value="5000">5s</SelectItem>
                <SelectItem value="10000">10s</SelectItem>
              </SelectContent>
            </Select>
            <Button variant="outline" size="sm" onClick={() => void refreshNow(sessionId)} disabled={loading}>
              {loading ? <Loader2 className="mr-1.5 size-4 animate-spin" /> : <RefreshCw className="mr-1.5 size-4" />}
              {t("terminal.serverStatus.refresh")}
            </Button>
          </div>
        </div>

        {/* 内容区（内部滚动） */}
        <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4">
          {error && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {t("terminal.serverStatus.error")}: {error}
            </div>
          )}

          {/* ① 资源使用 */}
          <section className="space-y-3">
            <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
              <Activity className="size-3.5" />
              {t("terminal.serverStatus.resourceUsage")}
            </div>
            <div className="grid gap-3 sm:grid-cols-3">
              <MetricCard
                icon={Cpu}
                tint="emerald"
                label={t("terminal.serverStatus.cpu")}
                value={formatPercent(cpuPercent)}
                valueClassName="text-success"
                caption={t("terminal.serverStatus.cpuDetail")}
              >
                {hasTrend ? (
                  <Sparkline
                    values={cpuSeries}
                    min={0}
                    max={Math.max(20, Math.max(...cpuSeries) * 1.2)}
                    color={METRIC_COLOR.cpu}
                    height={42}
                    className="w-full"
                  />
                ) : (
                  <Collecting label={t("terminal.serverStatus.collecting")} />
                )}
              </MetricCard>

              <MetricCard
                icon={MemoryStick}
                tint="amber"
                label={t("terminal.serverStatus.memory")}
                value={formatPercent(memoryPercent)}
                valueClassName="text-warning"
                caption={`${formatBytes(latest?.memoryUsedBytes)} / ${formatBytes(latest?.memoryTotalBytes)}`}
              >
                {hasTrend ? (
                  <Sparkline
                    values={memSeries}
                    min={30}
                    max={70}
                    color={METRIC_COLOR.memory}
                    height={42}
                    className="w-full"
                  />
                ) : (
                  <Collecting label={t("terminal.serverStatus.collecting")} />
                )}
              </MetricCard>

              <MetricCard
                icon={HardDrive}
                tint="sky"
                label={`${t("terminal.serverStatus.disk")} ${latest?.diskMount || "/"}`}
                value={formatPercent(diskPercent)}
                valueClassName="text-info"
                caption={`${formatBytes(latest?.diskUsedBytes)} / ${formatBytes(latest?.diskTotalBytes)}`}
              >
                <div className="mt-5 h-2 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-info transition-all"
                    style={{ width: `${Math.max(0, Math.min(100, diskPercent ?? 0))}%` }}
                  />
                </div>
              </MetricCard>
            </div>
          </section>

          {/* ② 系统负载 */}
          <section className="rounded-xl border bg-background/40 p-4">
            <div className="mb-3 flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-medium">
                <BarChart3 className="size-4 text-muted-foreground" />
                {t("terminal.serverStatus.loadAverage")}
              </div>
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>
                  {t("terminal.serverStatus.load1")}{" "}
                  <b className="ml-1 text-sm tabular-nums text-foreground">{formatLoad(latest?.load1)}</b>
                </span>
                <span>
                  {t("terminal.serverStatus.load5")}{" "}
                  <b className="ml-1 text-sm tabular-nums text-foreground">{formatLoad(latest?.load5)}</b>
                </span>
                <span>
                  {t("terminal.serverStatus.load15")}{" "}
                  <b className="ml-1 text-sm tabular-nums text-foreground">{formatLoad(latest?.load15)}</b>
                </span>
              </div>
            </div>
            {hasTrend ? (
              <Sparkline
                values={loadSeries}
                min={0}
                max={Math.max(0.4, Math.max(...loadSeries) * 1.3)}
                color={METRIC_COLOR.load}
                height={56}
                strokeWidth={2.5}
                className="w-full"
              />
            ) : (
              <Collecting label={t("terminal.serverStatus.collecting")} />
            )}
          </section>

          {/* ③ 详细信息（保留全部，默认展开） */}
          <section className="rounded-xl border bg-background/40">
            <button
              type="button"
              className="flex w-full items-center justify-between px-4 py-3 text-left"
              aria-expanded={detailsOpen}
              onClick={() => setDetailsOpen((v) => !v)}
            >
              <div className="flex items-center gap-2 text-sm font-medium">
                <Server className="size-4 text-muted-foreground" />
                {t("terminal.serverStatus.details")}
              </div>
              <div className="flex items-center gap-1 text-xs text-muted-foreground">
                <span>
                  {detailsOpen ? t("terminal.serverStatus.hideDetails") : t("terminal.serverStatus.showDetails")}
                </span>
                {detailsOpen ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
              </div>
            </button>
            {detailsOpen && (
              <dl className="grid gap-x-6 gap-y-3 border-t px-4 py-3 sm:grid-cols-2">
                <InfoRow label={t("terminal.serverStatus.host")} value={latest?.hostname || "-"} mono />
                <InfoRow label={t("terminal.serverStatus.os")} value={latest?.os || "-"} />
                <InfoRow
                  className="sm:col-span-2"
                  label={t("terminal.serverStatus.uptime")}
                  value={latest?.uptime || "-"}
                  mono
                />
                <InfoRow
                  label={t("terminal.serverStatus.memoryUsage")}
                  value={`${formatBytes(latest?.memoryUsedBytes)} / ${formatBytes(latest?.memoryTotalBytes)}`}
                />
                <InfoRow
                  label={t("terminal.serverStatus.diskUsage")}
                  value={`${formatBytes(latest?.diskUsedBytes)} / ${formatBytes(latest?.diskTotalBytes)}`}
                />
                <InfoRow label={t("terminal.serverStatus.diskMount")} value={latest?.diskMount || "/"} mono />
              </dl>
            )}
          </section>
        </div>
      </DialogContent>
    </Dialog>
  );
}

const TINT: Record<string, string> = {
  emerald: "bg-success/15 text-success",
  amber: "bg-warning/15 text-warning",
  sky: "bg-info/15 text-info",
};

function MetricCard({
  icon: Icon,
  tint,
  label,
  value,
  valueClassName,
  caption,
  children,
}: {
  icon: typeof Cpu;
  tint: string;
  label: string;
  value: string;
  valueClassName?: string;
  caption: string;
  children: ReactNode;
}) {
  return (
    <section className="rounded-xl border bg-background/40 p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className={`flex size-7 items-center justify-center rounded-lg ${TINT[tint] ?? TINT.emerald}`}>
            <Icon className="size-4" />
          </span>
          <span className="text-sm font-medium">{label}</span>
        </div>
        <span className={`text-xl font-semibold tabular-nums ${valueClassName ?? ""}`}>{value}</span>
      </div>
      {children}
      <div className="mt-1 text-[11px] text-muted-foreground">{caption}</div>
    </section>
  );
}

function Collecting({ label }: { label: string }) {
  return (
    <div className="mt-3 flex h-[42px] items-center justify-center text-[11px] text-muted-foreground">{label}</div>
  );
}

function InfoRow({
  label,
  value,
  mono,
  className,
}: {
  label: string;
  value: string;
  mono?: boolean;
  className?: string;
}) {
  return (
    <div className={className}>
      <dt className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</dt>
      <dd className={`mt-0.5 break-all text-sm ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  );
}
