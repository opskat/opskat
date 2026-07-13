/** 人类可读字节数：<1KiB 用整数 B，其余 1 位小数，单位阶梯 B/KB/MB/GB/TB。 */
export function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let val = size / 1024;
  let i = 0;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(1)} ${units[i]}`;
}

/** 传输速率：formatBytes + "/s"。 */
export function formatSpeed(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`;
}
