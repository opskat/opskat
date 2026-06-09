export type HealthLevel = "healthy" | "warning" | "critical" | "unknown";

export function formatPercent(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return "-";
  return `${value.toFixed(1)}%`;
}

export function formatBytes(value: number | undefined): string {
  if (!value || value <= 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const normalized = value / Math.pow(1024, index);
  return `${normalized.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

export function formatLoad(value: number | undefined): string {
  if (!value || value <= 0) return "-";
  return value.toFixed(2);
}

export function formatUptime(value: string | undefined, language = "en"): string {
  if (!value) return "-";

  const normalized = value.trim().replace(/\s+/g, " ");
  if (!normalized) return "-";

  const daysMatch = normalized.match(/\b(\d+)\s+days?\b/);
  const timeAfterDaysMatch = daysMatch ? normalized.slice(daysMatch.index ?? 0).match(/,\s*(\d+):(\d+)\b/) : null;
  const hoursMinutesMatch = normalized.match(/\bup\s+(\d+):(\d+)\b/);
  const minutesMatch = normalized.match(/\bup\s+(\d+)\s+mins?\b/);

  const days = daysMatch ? Number(daysMatch[1]) : 0;
  const hours = timeAfterDaysMatch
    ? Number(timeAfterDaysMatch[1])
    : hoursMinutesMatch
      ? Number(hoursMinutesMatch[1])
      : 0;
  const minutes = timeAfterDaysMatch
    ? Number(timeAfterDaysMatch[2])
    : hoursMinutesMatch
      ? Number(hoursMinutesMatch[2])
      : minutesMatch
        ? Number(minutesMatch[1])
        : 0;

  if (!days && !hours && !minutes) return normalized;

  const zh = language.toLowerCase().startsWith("zh");
  const parts: string[] = [];
  if (days) parts.push(zh ? `${days}天` : `${days}d`);
  if (hours) parts.push(zh ? `${hours}小时` : `${hours}h`);
  if (minutes || parts.length === 0) parts.push(zh ? `${minutes}分` : `${minutes}m`);
  return parts.join(" ");
}

export function usagePercent(used: number | undefined, total: number | undefined): number | null {
  if (!used || !total || total <= 0) return null;
  return Math.max(0, Math.min(100, (used / total) * 100));
}

interface Threshold {
  warn: number;
  crit: number;
}

// D6: CPU/内存 warn>=70 crit>=90；磁盘 warn>=80 crit>=92（磁盘 60% 不再误报）
const THRESHOLDS = {
  cpu: { warn: 70, crit: 90 },
  memory: { warn: 70, crit: 90 },
  disk: { warn: 80, crit: 92 },
} satisfies Record<string, Threshold>;

const RANK: Record<HealthLevel, number> = { unknown: -1, healthy: 0, warning: 1, critical: 2 };

function levelFor(percent: number | null, th: Threshold): HealthLevel {
  if (percent === null || !Number.isFinite(percent)) return "unknown";
  if (percent >= th.crit) return "critical";
  if (percent >= th.warn) return "warning";
  return "healthy";
}

export function getHealthLevel(cpu: number | null, memory: number | null, disk: number | null): HealthLevel {
  const levels = [
    levelFor(cpu, THRESHOLDS.cpu),
    levelFor(memory, THRESHOLDS.memory),
    levelFor(disk, THRESHOLDS.disk),
  ].filter((l): l is Exclude<HealthLevel, "unknown"> => l !== "unknown");
  if (!levels.length) return "unknown";
  return levels.reduce<HealthLevel>((worst, l) => (RANK[l] > RANK[worst] ? l : worst), "healthy");
}
