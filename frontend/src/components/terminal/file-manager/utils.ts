import { sftp_svc } from "../../../../wailsjs/go/models";

export const HANDLE_PX = 4;

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + " " + units[i];
}

export function formatDate(timestamp: number): string {
  const d = new Date(timestamp * 1000);
  return d.toLocaleDateString(undefined, {
    month: "2-digit",
    day: "2-digit",
  });
}

export function normalizeRemotePath(basePath: string, nextPath: string): string {
  const raw = nextPath.trim();
  if (!raw) return basePath || "/";
  const combined = raw.startsWith("/") ? raw : `${basePath === "/" ? "" : basePath}/${raw}`;
  const parts = combined.split("/");
  const normalized: string[] = [];
  for (const part of parts) {
    if (!part || part === ".") continue;
    if (part === "..") {
      normalized.pop();
      continue;
    }
    normalized.push(part);
  }
  return "/" + normalized.join("/");
}

export function getParentPath(currentPath: string): string {
  return currentPath.replace(/\/[^/]+\/?$/, "") || "/";
}

export function getPathBaseName(path: string): string {
  const normalized = normalizeRemotePath("/", path);
  if (normalized === "/") return "";
  const segments = normalized.split("/").filter(Boolean);
  return segments.at(-1) ?? "";
}

export function canMovePathToDirectory(sourcePath: string, targetDirPath: string): boolean {
  const source = normalizeRemotePath("/", sourcePath);
  const target = normalizeRemotePath("/", targetDirPath);
  if (!source || source === "/" || !target) return false;
  if (source === target) return false;
  // target 就是 source 自己所在的目录 —— 已经在那儿了,这一放什么也不会发生。
  // 更高的祖先目录不在此列:树里祖先与 ".." 都可见,拖到上面就是"往上挪一层/几层"这个正当动作。
  if (getParentPath(source) === target) return false;
  // target 落在 source 自己的子树里 —— 不能把目录挪进它自己的后代。
  return !target.startsWith(`${source}/`);
}

/** 树未达到面板宽度预算前每级的固定缩进增量;超预算后每级只增加极小量,靠竖直参考线表达层级。 */
export const TREE_INDENT_STEP_PX = 12;
export const TREE_INDENT_COMPACT_STEP_PX = 2;
export const TREE_ROW_PADDING_PX = 8;
/** 面板宽度里为图标、文件名与右侧大小/日期列预留的最小空间;超过这部分才允许缩进增长。 */
export const TREE_MIN_CONTENT_PX = 160;

/**
 * 按面板当前宽度计算某深度的左侧缩进(像素)。未超预算前是 depth * TREE_INDENT_STEP_PX 的
 * 固定增长;超过预算后每级只加 TREE_INDENT_COMPACT_STEP_PX,保证任何宽度下文件名都优先于
 * 缩进获得空间,不引入横向滚动。深度越深、面板越窄,封顶生效得越早。
 */
export function indentForDepth(depth: number, panelWidth: number): number {
  if (depth <= 0) return TREE_ROW_PADDING_PX;
  const budget = Math.max(0, panelWidth - TREE_MIN_CONTENT_PX);
  const capDepth = Math.floor(budget / TREE_INDENT_STEP_PX);
  if (depth <= capDepth) return TREE_ROW_PADDING_PX + depth * TREE_INDENT_STEP_PX;
  const overflowDepth = depth - capDepth;
  return TREE_ROW_PADDING_PX + capDepth * TREE_INDENT_STEP_PX + overflowDepth * TREE_INDENT_COMPACT_STEP_PX;
}

export function splitNameForRename(name: string): { stemLength: number } {
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return { stemLength: name.length };
  return { stemLength: dot };
}

export function getChildPath(parentPath: string, name: string): string {
  return parentPath === "/" ? "/" + name : parentPath + "/" + name;
}

export function joinRemotePath(parentPath: string, name: string): string {
  return normalizeRemotePath(parentPath, name);
}

export function sortEntries(entries: sftp_svc.FileEntry[]): sftp_svc.FileEntry[] {
  return [...entries].sort((a, b) => {
    if (a.isDir && !b.isDir) return -1;
    if (!a.isDir && b.isDir) return 1;
    return a.name.localeCompare(b.name);
  });
}
