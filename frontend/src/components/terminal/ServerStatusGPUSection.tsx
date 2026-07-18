import { useTranslation } from "react-i18next";
import { CircuitBoard, Fan, Thermometer, TriangleAlert, Zap } from "lucide-react";
import { cn } from "@opskat/ui";
import { formatBytes, formatPercent, usagePercent } from "@/components/terminal/serverStatusMetrics";
import type { ServerStatusGPU } from "@/stores/serverStatusStore";

interface ServerStatusGPUSectionProps {
  gpus: ServerStatusGPU[];
  driverVersion?: string;
  cudaVersion?: string;
}

export function ServerStatusGPUSection({ gpus, driverVersion, cudaVersion }: ServerStatusGPUSectionProps) {
  const { t } = useTranslation();

  if (gpus.length === 0) return null;

  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <CircuitBoard className="size-3.5" aria-hidden />
          {t("terminal.serverStatus.gpuAccelerators")}
          <span className="rounded-full bg-primary/15 px-2 py-0.5 text-[10px] font-medium text-primary">
            {t(gpus.length === 1 ? "terminal.serverStatus.gpuCountOne" : "terminal.serverStatus.gpuCountMany", {
              count: gpus.length,
            })}
          </span>
        </div>
        {(driverVersion || cudaVersion) && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
            {driverVersion && (
              <span className="inline-flex items-center gap-1">
                {t("terminal.serverStatus.driverVersion")}
                <b className="font-mono font-medium text-foreground">{driverVersion}</b>
              </span>
            )}
            {cudaVersion && (
              <span className="inline-flex items-center gap-1">
                {t("terminal.serverStatus.cudaVersion")}
                <b className="font-mono font-medium text-foreground">{cudaVersion}</b>
              </span>
            )}
          </div>
        )}
      </div>

      <div
        data-testid="server-status-gpu-list"
        className={cn("space-y-3", gpus.length > 2 && "max-h-[360px] overflow-y-auto pr-1")}
      >
        {gpus.map((gpu) => (
          <GPUCard key={`${gpu.vendor ?? "gpu"}-${gpu.index}`} gpu={gpu} />
        ))}
      </div>
    </section>
  );
}

function GPUCard({ gpu }: { gpu: ServerStatusGPU }) {
  const { t } = useTranslation();
  const memoryPercent = usagePercent(gpu.memoryUsedBytes, gpu.memoryTotalBytes);
  const displayName = gpu.name || gpu.vendor || "-";

  return (
    <article className="rounded-xl border bg-background/40 p-4">
      <div className="flex min-w-0 items-center gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
          <CircuitBoard className="size-[18px]" aria-hidden />
        </span>
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold">
            GPU {gpu.index} · {displayName}
          </h3>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{gpu.vendor || "-"}</p>
        </div>
      </div>

      <div className="mt-4 grid grid-cols-[minmax(0,1fr)_minmax(0,1.35fr)] gap-4">
        <GPUProgress
          label={t("terminal.serverStatus.gpuUtilization")}
          value={formatPercent(metricNumber(gpu.utilizationPercent))}
          percent={metricNumber(gpu.utilizationPercent)}
          indicatorClassName="bg-primary"
          valueClassName="text-primary"
        />
        <GPUProgress
          label={t("terminal.serverStatus.vramUsage")}
          value={`${formatOptionalBytes(gpu.memoryUsedBytes)} / ${formatOptionalBytes(gpu.memoryTotalBytes)}`}
          percent={memoryPercent}
          indicatorClassName="bg-info"
          valueClassName="text-info"
        />
        <dl className="col-span-2 grid grid-cols-4 gap-3 border-t pt-3">
          <TemperatureMetric value={gpu.temperatureC} />
          <SecondaryMetric
            icon={Zap}
            label={t("terminal.serverStatus.power")}
            value={`${formatWatts(gpu.powerDrawWatts)} / ${formatWatts(gpu.powerLimitWatts)}`}
          />
          <SecondaryMetric
            icon={Fan}
            label={t("terminal.serverStatus.fan")}
            value={formatPercent(metricNumber(gpu.fanPercent))}
          />
          <SecondaryMetric
            icon={CircuitBoard}
            label={t("terminal.serverStatus.computeProcesses")}
            value={formatInteger(gpu.computeProcessCount)}
          />
        </dl>
      </div>
    </article>
  );
}

function GPUProgress({
  label,
  value,
  percent,
  indicatorClassName,
  valueClassName,
}: {
  label: string;
  value: string;
  percent: number | null;
  indicatorClassName: string;
  valueClassName: string;
}) {
  return (
    <div>
      <div className="flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
        <span>{label}</span>
        <span className={cn("font-semibold tabular-nums", valueClassName)}>{value}</span>
      </div>
      <div className="mt-2 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full transition-[width] duration-200", indicatorClassName)}
          style={{ width: `${clampPercent(percent)}%` }}
        />
      </div>
    </div>
  );
}

function TemperatureMetric({ value }: { value?: number }) {
  const { t } = useTranslation();
  const temperature = metricNumber(value);
  const level = temperature === null ? null : temperature >= 90 ? "critical" : temperature >= 80 ? "warning" : null;

  return (
    <div className="min-w-0">
      <dt className="flex items-center gap-1 text-[10px] text-muted-foreground">
        <Thermometer className="size-3" aria-hidden />
        {t("terminal.serverStatus.temperature")}
      </dt>
      <dd
        className={cn(
          "mt-1 flex items-center gap-1 text-sm font-semibold tabular-nums",
          level === "critical" && "text-destructive",
          level === "warning" && "text-warning",
          temperature === null && "text-muted-foreground"
        )}
      >
        {formatTemperature(temperature)}
        {level && (
          <span className="inline-flex items-center gap-0.5 text-[10px] font-medium">
            <TriangleAlert className="size-3" aria-hidden />
            {t(`terminal.serverStatus.${level}`)}
          </span>
        )}
      </dd>
    </div>
  );
}

function SecondaryMetric({ icon: Icon, label, value }: { icon: typeof Zap; label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="flex items-center gap-1 text-[10px] text-muted-foreground">
        <Icon className="size-3" aria-hidden />
        <span className="truncate" title={label}>
          {label}
        </span>
      </dt>
      <dd className="mt-1 whitespace-nowrap text-sm font-semibold tabular-nums">{value}</dd>
    </div>
  );
}

function metricNumber(value: number | undefined): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function clampPercent(value: number | null): number {
  if (value === null) return 0;
  return Math.max(0, Math.min(100, value));
}

function formatOptionalBytes(value: number | undefined): string {
  const number = metricNumber(value);
  if (number === null || number < 0) return "-";
  if (number === 0) return "0 B";
  return formatBytes(number);
}

function formatTemperature(value: number | null): string {
  if (value === null) return "-";
  return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1)}°C`;
}

function formatWatts(value: number | undefined): string {
  const number = metricNumber(value);
  if (number === null || number < 0) return "-";
  return `${Number.isInteger(number) ? number.toFixed(0) : number.toFixed(1)} W`;
}

function formatInteger(value: number | undefined): string {
  const number = metricNumber(value);
  if (number === null || number < 0) return "-";
  return Math.round(number).toString();
}
