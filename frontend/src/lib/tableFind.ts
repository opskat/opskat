import { cellValueToDisplayText } from "@/lib/cellValue";

/**
 * 表格内查找（#328）的纯逻辑：只在当前已加载的那一页里，按表格显示的行序 / 列序匹配单元格的显示文本。
 */

/** 结果区根节点上的标记：焦点在其内时全局 ⌘F（panel.filter）让路，由表格自己打开查找条。 */
export const TABLE_FIND_SCOPE_ATTR = "data-table-find-scope";

export interface TableFindOptions {
  caseSensitive: boolean;
  wholeCell: boolean;
  regex: boolean;
}

/** 半开区间 [start, end)，下标基于单元格的显示文本。 */
export type TableFindRange = readonly [start: number, end: number];

export interface TableFindMatch {
  rowIdx: number;
  col: string;
  ranges: TableFindRange[];
}

export interface TableFindResult {
  matches: TableFindMatch[];
  error?: "invalidRegex";
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function buildPattern(query: string, options: TableFindOptions): RegExp | null {
  const source = options.regex ? query : escapeRegExp(query);
  const anchored = options.wholeCell ? `^(?:${source})$` : source;
  try {
    return new RegExp(anchored, options.caseSensitive ? "gu" : "giu");
  } catch {
    return null;
  }
}

function rangesIn(text: string, pattern: RegExp): TableFindRange[] {
  const ranges: TableFindRange[] = [];
  pattern.lastIndex = 0;
  for (let m = pattern.exec(text); m; m = pattern.exec(text)) {
    if (m[0].length === 0) {
      pattern.lastIndex += 1;
      continue;
    }
    ranges.push([m.index, m.index + m[0].length]);
  }
  return ranges;
}

/**
 * @param rows        当前页的行（含未保存的新增行），rowIdx 即其下标
 * @param displayColumns 可见列，按表格显示顺序
 * @param edits       未提交的单元格修改，key 为 `${rowIdx}:${col}`；有修改时按修改后的值匹配
 * @param rowOrder    表格当前显示的行序（客户端排序 / 过滤之后）；省略时按 rows 原顺序
 */
export function findMatches(
  rows: readonly Record<string, unknown>[],
  displayColumns: readonly string[],
  edits: ReadonlyMap<string, unknown> | undefined,
  query: string,
  options: TableFindOptions,
  rowOrder?: readonly number[]
): TableFindResult {
  if (!query) return { matches: [] };
  const pattern = buildPattern(query, options);
  if (!pattern) return { matches: [], error: "invalidRegex" };

  const matches: TableFindMatch[] = [];
  const visit = (rowIdx: number) => {
    const row = rows[rowIdx];
    for (const col of displayColumns) {
      const key = `${rowIdx}:${col}`;
      const value = edits?.has(key) ? edits.get(key) : row[col];
      // NULL 的「NULL」只是占位，不参与匹配
      if (value == null) continue;
      const ranges = rangesIn(cellValueToDisplayText(value), pattern);
      if (ranges.length > 0) matches.push({ rowIdx, col, ranges });
    }
  };
  if (rowOrder) rowOrder.forEach(visit);
  else rows.forEach((_, rowIdx) => visit(rowIdx));
  return { matches };
}

/**
 * 一维「滚进可视区」：返回滚动位置需要增加的量（负数为回滚），0 表示已完整可见。
 * viewStart 已扣除粘性区域（行号列 + 冻结列 / 表头），所以被冻结列遮住的格也会被滚出来。
 * 目标比可视区还宽时优先保证起点可见。
 */
export function revealDelta(itemStart: number, itemEnd: number, viewStart: number, viewEnd: number): number {
  if (itemStart < viewStart) return itemStart - viewStart;
  if (itemEnd > viewEnd) return Math.min(itemEnd - viewEnd, itemStart - viewStart);
  return 0;
}
