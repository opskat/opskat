import { group_entity } from "../../../wailsjs/go/models";
import type { Row } from "./flattenTree";

export type InsertionPoint =
  | { kind: "before-group"; groupID: number; depth: number }
  | { kind: "after-group"; groupID: number; depth: number }
  | { kind: "into-group-first"; groupID: number; depth: number }
  | { kind: "before-asset"; assetID: number; groupID: number; depth: number }
  | { kind: "after-asset"; assetID: number; groupID: number; depth: number }
  | { kind: "root-end" }
  | { kind: "invalid" };

export interface RowRect {
  top: number;
  bottom: number;
  height: number;
}

export type DragActive = { kind: "asset"; id: number; ids?: number[] } | { kind: "group"; id: number };

export interface ComputeInsertionPointInput {
  rows: Row[];
  rowRects: Map<string, RowRect>;
  pointerY: number;
  active: DragActive;
  groups: group_entity.Group[];
  /** 搜索或类型筛选生效时，仅允许拖入目标分组/未分组，禁止组内精细插队排序。 */
  isFiltered?: boolean;
  /** 拖拽已分组资产且当前树无“未分组”桶时，底部的动态“移至未分组”投放区矩形。 */
  ungroupedZoneRect?: RowRect | null;
}

export function rowKey(row: Row): string {
  switch (row.kind) {
    case "group-header":
      return `group-${row.groupID}`;
    case "asset":
      return `asset-${row.assetID}`;
    case "empty-placeholder":
      return `empty-${row.groupID}`;
  }
}

function isGroupSelfOrDescendant(targetGroupID: number, ancestorID: number, groups: group_entity.Group[]): boolean {
  if (targetGroupID === ancestorID) return true;
  const byID = new Map(groups.map((g) => [g.ID, g] as const));
  let cur = byID.get(targetGroupID);
  while (cur) {
    const p = cur.ParentID ?? 0;
    if (p === ancestorID) return true;
    if (p === 0) return false;
    cur = byID.get(p);
  }
  return false;
}

export function computeInsertionPoint({
  rows,
  rowRects,
  pointerY,
  active,
  groups,
  isFiltered = false,
  ungroupedZoneRect = null,
}: ComputeInsertionPointInput): InsertionPoint {
  if (
    active.kind === "asset" &&
    ungroupedZoneRect &&
    pointerY >= ungroupedZoneRect.top &&
    pointerY <= ungroupedZoneRect.bottom
  ) {
    return { kind: "into-group-first", groupID: 0, depth: 0 };
  }

  if (rows.length === 0) return active.kind === "group" && !isFiltered ? { kind: "root-end" } : { kind: "invalid" };

  if (isFiltered && active.kind === "group") {
    return { kind: "invalid" };
  }

  let hitIdx = -1;
  for (let i = 0; i < rows.length; i++) {
    const rect = rowRects.get(rowKey(rows[i]));
    if (!rect) continue;
    if (pointerY >= rect.top && pointerY < rect.bottom) {
      hitIdx = i;
      break;
    }
  }

  if (hitIdx < 0) {
    // pointer 超出最后一行：仅允许 group 落到 root 末尾；
    // asset 不走 root-end，避免无意中拖出原 group → 落入未分组。
    return active.kind === "group" && !isFiltered ? { kind: "root-end" } : { kind: "invalid" };
  }

  const row = rows[hitIdx];
  const rect = rowRects.get(rowKey(row))!;
  const upperHalf = pointerY < rect.top + rect.height / 2;
  const activeAssetSet =
    active.kind === "asset" ? new Set(active.ids && active.ids.length > 0 ? active.ids : [active.id]) : null;

  switch (row.kind) {
    case "asset": {
      if (active.kind === "group") return { kind: "invalid" };
      if (isFiltered) return { kind: "invalid" };
      if (activeAssetSet?.has(row.assetID)) return { kind: "invalid" };
      return upperHalf
        ? { kind: "before-asset", assetID: row.assetID, groupID: row.groupID, depth: row.depth }
        : { kind: "after-asset", assetID: row.assetID, groupID: row.groupID, depth: row.depth };
    }
    case "group-header": {
      if (row.groupID === 0) {
        if (active.kind === "group") return { kind: "invalid" };
        return { kind: "into-group-first", groupID: 0, depth: row.depth };
      }
      // asset 拖到任意 group header（无论上中下区）一律视为"进入该 group"。
      // 不让 asset 走 before-group → 否则顶级 group 的 ParentID=0 会把 asset 误推到未分组。
      if (active.kind === "asset") {
        return { kind: "into-group-first", groupID: row.groupID, depth: row.depth };
      }
      if (isGroupSelfOrDescendant(row.groupID, active.id, groups)) {
        return { kind: "invalid" };
      }
      // 三段热区：上 25% 插入该分组之前，中 50% 移入成为子分组，下 25% 插入该分组之后。
      // 展开且下方紧跟着它自己的子项时，标题下沿就是"第一个子项之前"——排到整棵子树之后
      // 会与画在标题下沿的提示线不符，所以这一段也按移入处理。
      const offsetY = pointerY - rect.top;
      if (offsetY < rect.height * 0.25) {
        return { kind: "before-group", groupID: row.groupID, depth: row.depth };
      }
      const next = rows[hitIdx + 1];
      const hasVisibleChildren = !row.collapsed && next !== undefined && next.depth > row.depth;
      if (offsetY >= rect.height * 0.75 && !hasVisibleChildren) {
        return { kind: "after-group", groupID: row.groupID, depth: row.depth };
      }
      return { kind: "into-group-first", groupID: row.groupID, depth: row.depth };
    }
    case "empty-placeholder": {
      if (active.kind === "group" && isGroupSelfOrDescendant(row.groupID, active.id, groups)) {
        return { kind: "invalid" };
      }
      return { kind: "into-group-first", groupID: row.groupID, depth: row.depth };
    }
  }
}

/**
 * 拖拽悬停在折叠分组上时应自动展开的分组：只有"插到它前面"和"移入它"需要看见里面。
 * after-group 是"排在它后面"，展开会把同一位置的落点语义换成移入，因此不展开。
 */
export function hoverExpandGroupID(point: InsertionPoint): number | null {
  return point.kind === "before-group" || point.kind === "into-group-first" ? point.groupID : null;
}
