/**
 * 用 Intl.RelativeTimeFormat 把 Unix 秒时间戳格式化为相对现在的时间（「3 分钟前」「2 分钟后」）；
 * 超过一周的过去时间显示为月/日。
 */
export function formatRelativeTime(timestamp: number, locale: string, nowMs: number = Date.now()): string {
  const diff = timestamp - nowMs / 1000;
  const abs = Math.abs(diff);
  const sign = diff < 0 ? -1 : 1;
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  if (abs < 60) return rtf.format(0, "second");
  if (abs < 3600) return rtf.format(sign * Math.floor(abs / 60), "minute");
  if (abs < 86400) return rtf.format(sign * Math.floor(abs / 3600), "hour");
  if (abs < 604800 || sign > 0) return rtf.format(sign * Math.floor(abs / 86400), "day");
  const date = new Date(timestamp * 1000);
  return new Intl.DateTimeFormat(locale, { month: "numeric", day: "numeric" }).format(date);
}
