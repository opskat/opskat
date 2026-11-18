/** 把 ISO 时间（如 RFC3339 UTC）按本地时区格式化为 YYYY-MM-DD HH:mm，与语言无关。 */
export function formatLocalDateTime(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
