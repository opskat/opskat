/**
 * 路径或对象 key 的最后一段：同时认 / 与 \（本地路径可能是 Windows 路径），忽略结尾分隔符。
 */
export function pathBaseName(path: string): string {
  const trimmed = path.replace(/[/\\]+$/, "");
  const i = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return i >= 0 ? trimmed.slice(i + 1) : trimmed;
}
