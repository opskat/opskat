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

/** 粗略按等宽估算的每字符像素:名字与折叠链的宽度预算都按它折算,不做逐字测量。 */
export const AVG_CHAR_PX = 6.5;
/** 折叠链里一段的展开按钮(14px)加它后面的 gap(4px)。 */
const CHAIN_TOGGLE_PX = 18;
/** 段与段之间的 "/" 分隔符连同 gap。 */
const CHAIN_SEPARATOR_PX = 8;
/** 链首那一项前面的文件夹图标(14px)加 gap(4px),整行只画一次。 */
const CHAIN_LEAD_ICON_PX = 18;
/** 末段名字能被 middleEllipsisName 压到的字符下限(与它的 Math.max(8, …) 一致)。 */
const CHAIN_LAST_NAME_MIN_CHARS = 8;

/** 折叠链的一个渲染项:某一段本身,或代表被省略中段的省略号(index 是其中最深的一段)。 */
export type ChainRenderItem = { kind: "segment" | "ellipsis"; index: number };

export interface ChainRenderPlan {
  /** 按渲染次序的项;省略号最多一个,末段恒为最后一项。 */
  items: ChainRenderItem[];
  /** items 的预估总宽,以及行内留给链的可用宽度。 */
  width: number;
  available: number;
}

/**
 * 按面板当前宽度决定折叠链渲染哪几段:整条链放得下就全画;放不下则保留首段与最深段、中段折进
 * 一个省略号(长名字的中段省略,同一形状用在链上)。最深段是用户接着往下走的入口,必须永远在行内
 * 且可点 —— 连首段都放不下时首段也并进省略号。省略号自己带着被省略的最深一段:点它换根过去,
 * 被省略的目录不会变得无法抵达。宽度只按传入的 panelWidth 折算,不引入第二套测量。
 * names 至少两段 —— 单段行(普通目录/文件)根本不走链渲染。
 */
export function planChainRender(names: string[], depth: number, panelWidth: number, chromePx: number): ChainRenderPlan {
  const available = Math.max(0, panelWidth - indentForDepth(depth, panelWidth) - chromePx);
  const last = names.length - 1;
  const segmentPx = (index: number) =>
    index === last
      ? CHAIN_TOGGLE_PX + Math.min(names[index].length, CHAIN_LAST_NAME_MIN_CHARS) * AVG_CHAR_PX
      : CHAIN_TOGGLE_PX + names[index].length * AVG_CHAR_PX + CHAIN_SEPARATOR_PX;
  const ellipsisPx = CHAIN_TOGGLE_PX + AVG_CHAR_PX + CHAIN_SEPARATOR_PX;
  const planOf = (items: ChainRenderItem[]): ChainRenderPlan => ({
    items,
    width:
      CHAIN_LEAD_ICON_PX +
      items.reduce((sum, item) => sum + (item.kind === "ellipsis" ? ellipsisPx : segmentPx(item.index)), 0),
    available,
  });

  const full = planOf(names.map((_, index): ChainRenderItem => ({ kind: "segment", index })));
  if (full.width <= available) return full;
  if (names.length > 2) {
    const elidedMiddle = planOf([
      { kind: "segment", index: 0 },
      { kind: "ellipsis", index: last - 1 },
      { kind: "segment", index: last },
    ]);
    if (elidedMiddle.width <= available) return elidedMiddle;
  }
  return planOf([
    { kind: "ellipsis", index: last - 1 },
    { kind: "segment", index: last },
  ]);
}

export interface TreeGuideLine {
  /** 这条线代表的祖先层级(0 = 当前根那一层)。 */
  depth: number;
  /** 该祖先的缩进位置(行内左侧偏移像素),与那一层行的内容起点对齐。 */
  left: number;
}

/**
 * depth 行的每一级祖先各一条竖直参考线,位置就是那一级自己的缩进位置。参考线必须按祖先
 * 的缩进定位(行元素上的 border-l 画的是面板边缘那一条,表达不了任何层级归属)。
 */
export function treeGuideLines(depth: number, panelWidth: number): TreeGuideLine[] {
  const lines: TreeGuideLine[] = [];
  for (let d = 0; d < depth; d += 1) lines.push({ depth: d, left: indentForDepth(d, panelWidth) });
  return lines;
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
