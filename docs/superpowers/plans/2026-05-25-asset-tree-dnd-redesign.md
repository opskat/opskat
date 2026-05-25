# AssetTree DnD 重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 VS Code/Notion 风格的 insertion-point + 蓝线指示器模型替换 AssetTree 当前的 `closestCenter` over-based DnD 实现，根治 group 边界处 over 选错、上拖到末尾失败、无视觉反馈三类问题。

**Architecture:** 树扁平化为 `Row[]`，dragMove 用 pointer Y vs 每个 row 的 DOMRect 算出唯一 `InsertionPoint`，绑定到 Zustand store；`<DropIndicator>` 订阅 store 渲染一条蓝线；dragEnd 把 InsertionPoint 翻译为 `ReorderAsset/ReorderGroup` 的入参。`useSortable` 仅用于拿 drag listeners 和 row 节点 ref，不应用 transform。

**Tech Stack:** React 19、@dnd-kit/core 6.x（不用 SortableContext 的 strategy）、Zustand 5、vitest + happy-dom + RTL。

**Spec:** `docs/superpowers/specs/2026-05-25-asset-tree-dnd-redesign-design.md`

---

## File Structure

**Create:**
- `frontend/src/lib/assetTreeDnd/flattenTree.ts`
- `frontend/src/lib/assetTreeDnd/insertionPoint.ts`
- `frontend/src/lib/assetTreeDnd/reorderArgs.ts`
- `frontend/src/lib/assetTreeDnd/store.ts`
- `frontend/src/lib/assetTreeDnd/index.ts`
- `frontend/src/lib/assetTreeDnd/__tests__/flattenTree.test.ts`
- `frontend/src/lib/assetTreeDnd/__tests__/insertionPoint.test.ts`
- `frontend/src/lib/assetTreeDnd/__tests__/reorderArgs.test.ts`

**Modify:**
- `frontend/src/components/layout/AssetTree.tsx` — 整段 DnD 改造

**Delete:**
- `frontend/src/lib/assetTreeReorder.ts`
- `frontend/src/__tests__/assetTreeReorder.test.ts`

---

## Task 1: 扁平化 `flattenTree`

**Files:**
- Create: `frontend/src/lib/assetTreeDnd/flattenTree.ts`
- Create: `frontend/src/lib/assetTreeDnd/__tests__/flattenTree.test.ts`

- [ ] **Step 1.1: 写失败测试**

写入 `frontend/src/lib/assetTreeDnd/__tests__/flattenTree.test.ts`：

```ts
import { describe, it, expect } from "vitest";
import { flattenTree } from "../flattenTree";
import { asset_entity, group_entity } from "@/wailsjs/go/models";

function g(id: number, parentID = 0): group_entity.Group {
  return new group_entity.Group({ ID: id, ParentID: parentID, Status: 1 });
}
function a(id: number, groupID: number): asset_entity.Asset {
  return new asset_entity.Asset({ ID: id, GroupID: groupID, Status: 1 });
}

describe("flattenTree", () => {
  it("根级 group 按顺序 → header 在前、内部 children 先 sub-group 后 asset", () => {
    const rows = flattenTree({
      groups: [g(1), g(2)],
      assets: [a(10, 1), a(11, 1), a(20, 2)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows).toEqual([
      { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
      { kind: "asset", assetID: 10, groupID: 1, depth: 1 },
      { kind: "asset", assetID: 11, groupID: 1, depth: 1 },
      { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
      { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
    ]);
  });

  it("嵌套 group: 父 group 的 child group 先 push，再 push 父 group 的直接 asset", () => {
    const rows = flattenTree({
      groups: [g(1), g(11, 1)],
      assets: [a(100, 1), a(110, 11)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows.map((r) => `${r.kind}-${"groupID" in r ? r.groupID : "assetID" in r ? r.assetID : "?"}`)).toEqual([
      "group-header-1",
      "group-header-11",
      "asset-110",
      "asset-100",
    ]);
  });

  it("折叠 group → 不展开其子项，但 header 仍然 push", () => {
    const rows = flattenTree({
      groups: [g(1), g(2)],
      assets: [a(10, 1), a(20, 2)],
      collapsedGroupIDs: new Set([1]),
      shouldHideEmpty: false,
    });
    expect(rows).toEqual([
      { kind: "group-header", groupID: 1, depth: 0, collapsed: true },
      { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
      { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
    ]);
  });

  it("空 group 展开 → 出现 empty-placeholder", () => {
    const rows = flattenTree({
      groups: [g(1)],
      assets: [],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows).toEqual([
      { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
      { kind: "empty-placeholder", groupID: 1, depth: 1 },
    ]);
  });

  it("shouldHideEmpty=true → 整个空 group 不出现", () => {
    const rows = flattenTree({
      groups: [g(1), g(2)],
      assets: [a(20, 2)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: true,
    });
    expect(rows.find((r) => r.kind === "group-header" && r.groupID === 1)).toBeUndefined();
    expect(rows.find((r) => r.kind === "group-header" && r.groupID === 2)).toBeDefined();
  });

  it("未分组桶: 有 root assets → 末尾追加 group-header(0) + 其 assets", () => {
    const rows = flattenTree({
      groups: [g(1)],
      assets: [a(10, 1), a(99, 0)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows[rows.length - 2]).toEqual({ kind: "group-header", groupID: 0, depth: 0, collapsed: false });
    expect(rows[rows.length - 1]).toEqual({ kind: "asset", assetID: 99, groupID: 0, depth: 1 });
  });
});
```

- [ ] **Step 1.2: 跑测试验证失败**

```bash
cd frontend && pnpm test -- --run src/lib/assetTreeDnd/__tests__/flattenTree.test.ts
```

Expected: 全部 FAIL（`Cannot find module '../flattenTree'`）

- [ ] **Step 1.3: 写实现**

写入 `frontend/src/lib/assetTreeDnd/flattenTree.ts`：

```ts
import { asset_entity, group_entity } from "@/wailsjs/go/models";

export type Row =
  | { kind: "group-header"; groupID: number; depth: number; collapsed: boolean }
  | { kind: "asset"; assetID: number; groupID: number; depth: number }
  | { kind: "empty-placeholder"; groupID: number; depth: number };

export interface FlattenInput {
  groups: group_entity.Group[];
  assets: asset_entity.Asset[];
  collapsedGroupIDs: Set<number>;
  shouldHideEmpty: boolean;
}

export function flattenTree({ groups, assets, collapsedGroupIDs, shouldHideEmpty }: FlattenInput): Row[] {
  const childrenByParent = new Map<number, group_entity.Group[]>();
  for (const g of groups) {
    const p = g.ParentID ?? 0;
    if (!childrenByParent.has(p)) childrenByParent.set(p, []);
    childrenByParent.get(p)!.push(g);
  }
  const assetsByGroup = new Map<number, asset_entity.Asset[]>();
  for (const a of assets) {
    const gid = a.GroupID ?? 0;
    if (!assetsByGroup.has(gid)) assetsByGroup.set(gid, []);
    assetsByGroup.get(gid)!.push(a);
  }

  const countAssetsInGroup = (groupID: number): number => {
    let n = (assetsByGroup.get(groupID) || []).length;
    for (const c of childrenByParent.get(groupID) || []) n += countAssetsInGroup(c.ID);
    return n;
  };

  const rows: Row[] = [];
  const walk = (parentID: number, depth: number) => {
    for (const grp of childrenByParent.get(parentID) || []) {
      if (shouldHideEmpty && countAssetsInGroup(grp.ID) === 0) continue;
      const collapsed = collapsedGroupIDs.has(grp.ID);
      rows.push({ kind: "group-header", groupID: grp.ID, depth, collapsed });
      if (collapsed) continue;
      const before = rows.length;
      walk(grp.ID, depth + 1);
      for (const a of assetsByGroup.get(grp.ID) || []) {
        rows.push({ kind: "asset", assetID: a.ID, groupID: grp.ID, depth: depth + 1 });
      }
      if (rows.length === before) {
        rows.push({ kind: "empty-placeholder", groupID: grp.ID, depth: depth + 1 });
      }
    }
  };
  walk(0, 0);

  const rootAssets = assetsByGroup.get(0) || [];
  if (rootAssets.length > 0) {
    const collapsed = collapsedGroupIDs.has(0);
    rows.push({ kind: "group-header", groupID: 0, depth: 0, collapsed });
    if (!collapsed) {
      for (const a of rootAssets) {
        rows.push({ kind: "asset", assetID: a.ID, groupID: 0, depth: 1 });
      }
    }
  }
  return rows;
}
```

- [ ] **Step 1.4: 跑测试验证通过**

```bash
cd frontend && pnpm test -- --run src/lib/assetTreeDnd/__tests__/flattenTree.test.ts
```

Expected: 6 passed

- [ ] **Step 1.5: Commit**

```bash
git add frontend/src/lib/assetTreeDnd/flattenTree.ts frontend/src/lib/assetTreeDnd/__tests__/flattenTree.test.ts
git commit -m "✨ AssetTree DnD: 扁平化函数 flattenTree"
```

---

## Task 2: 插入点判定 `computeInsertionPoint`

**Files:**
- Create: `frontend/src/lib/assetTreeDnd/insertionPoint.ts`
- Create: `frontend/src/lib/assetTreeDnd/__tests__/insertionPoint.test.ts`

- [ ] **Step 2.1: 写失败测试**

写入 `frontend/src/lib/assetTreeDnd/__tests__/insertionPoint.test.ts`：

```ts
import { describe, it, expect } from "vitest";
import { computeInsertionPoint, rowKey } from "../insertionPoint";
import type { Row } from "../flattenTree";
import { group_entity } from "@/wailsjs/go/models";

function g(id: number, parentID = 0): group_entity.Group {
  return new group_entity.Group({ ID: id, ParentID: parentID, Status: 1 });
}

function makeRects(rows: Row[], rowHeight = 24): Map<string, { top: number; bottom: number; height: number }> {
  const rects = new Map<string, { top: number; bottom: number; height: number }>();
  rows.forEach((row, idx) => {
    rects.set(rowKey(row), { top: idx * rowHeight, bottom: (idx + 1) * rowHeight, height: rowHeight });
  });
  return rects;
}

describe("computeInsertionPoint - asset active", () => {
  const groups = [g(1), g(2)];
  const rows: Row[] = [
    { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
    { kind: "asset", assetID: 10, groupID: 1, depth: 1 },
    { kind: "asset", assetID: 11, groupID: 1, depth: 1 },
    { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
    { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
  ];
  const rects = makeRects(rows);

  it("asset 上半区 → before-asset", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 5, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "before-asset", assetID: 10, groupID: 1, depth: 1 });
  });

  it("asset 下半区（非末尾）→ after-asset", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 18, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "after-asset", assetID: 10, groupID: 1, depth: 1 });
  });

  it("asset 下半区（组末尾）→ after-asset（reorderArgs 阶段映射成末尾）", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 48 + 18, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "after-asset", assetID: 11, groupID: 1, depth: 1 });
  });

  it("group header 上半区 → before-group", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 72 + 5, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "before-group", groupID: 2, depth: 0 });
  });

  it("group header 下半区 → into-group-first", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 72 + 18, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "into-group-first", groupID: 2, depth: 0 });
  });

  it("pointer 超出树底部 → root-end", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 9999, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "root-end" });
  });

  it("拖到自己的当前 row → invalid", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 5, active: { kind: "asset", id: 10 }, groups })
    ).toEqual({ kind: "invalid" });
  });
});

describe("computeInsertionPoint - group active", () => {
  const groups = [g(1), g(11, 1), g(2)];
  const rows: Row[] = [
    { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
    { kind: "group-header", groupID: 11, depth: 1, collapsed: true },
    { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
  ];
  const rects = makeRects(rows);

  it("group 拖到 asset row → invalid", () => {
    const rowsWithAsset: Row[] = [
      ...rows,
      { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
    ];
    const r = makeRects(rowsWithAsset);
    expect(
      computeInsertionPoint({
        rows: rowsWithAsset,
        rowRects: r,
        pointerY: 72 + 5,
        active: { kind: "group", id: 11 },
        groups,
      })
    ).toEqual({ kind: "invalid" });
  });

  it("group 拖到自己或自己的子孙 → invalid", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 5, active: { kind: "group", id: 1 }, groups })
    ).toEqual({ kind: "invalid" });
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 18, active: { kind: "group", id: 1 }, groups })
    ).toEqual({ kind: "invalid" });
  });

  it("group 拖到 ungrouped 桶 header → invalid（group 不能嵌入未分组）", () => {
    const ungroupedRows: Row[] = [{ kind: "group-header", groupID: 0, depth: 0, collapsed: false }];
    const r = makeRects(ungroupedRows);
    expect(
      computeInsertionPoint({
        rows: ungroupedRows,
        rowRects: r,
        pointerY: 18,
        active: { kind: "group", id: 1 },
        groups,
      })
    ).toEqual({ kind: "invalid" });
  });
});

describe("computeInsertionPoint - empty placeholder & ungrouped", () => {
  const groups = [g(1)];
  const rows: Row[] = [
    { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
    { kind: "empty-placeholder", groupID: 1, depth: 1 },
  ];
  const rects = makeRects(rows);

  it("empty-placeholder → into-group-first", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 12, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "into-group-first", groupID: 1, depth: 1 });
  });

  it("ungrouped 桶 header（任意半区，asset active）→ into-group-first", () => {
    const ungroupedRows: Row[] = [{ kind: "group-header", groupID: 0, depth: 0, collapsed: false }];
    const r = makeRects(ungroupedRows);
    expect(
      computeInsertionPoint({
        rows: ungroupedRows,
        rowRects: r,
        pointerY: 5,
        active: { kind: "asset", id: 99 },
        groups,
      })
    ).toEqual({ kind: "into-group-first", groupID: 0, depth: 0 });
  });
});
```

- [ ] **Step 2.2: 跑测试验证失败**

```bash
cd frontend && pnpm test -- --run src/lib/assetTreeDnd/__tests__/insertionPoint.test.ts
```

Expected: 全部 FAIL（找不到模块）

- [ ] **Step 2.3: 写实现**

写入 `frontend/src/lib/assetTreeDnd/insertionPoint.ts`：

```ts
import { group_entity } from "@/wailsjs/go/models";
import type { Row } from "./flattenTree";

export type InsertionPoint =
  | { kind: "before-group"; groupID: number; depth: number }
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

export interface ComputeInsertionPointInput {
  rows: Row[];
  rowRects: Map<string, RowRect>;
  pointerY: number;
  active: { kind: "asset" | "group"; id: number };
  groups: group_entity.Group[];
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

function isGroupSelfOrDescendant(
  targetGroupID: number,
  ancestorID: number,
  groups: group_entity.Group[]
): boolean {
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
}: ComputeInsertionPointInput): InsertionPoint {
  if (rows.length === 0) return { kind: "root-end" };

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
    return { kind: "root-end" };
  }

  const row = rows[hitIdx];
  const rect = rowRects.get(rowKey(row))!;
  const upperHalf = pointerY < rect.top + rect.height / 2;

  switch (row.kind) {
    case "asset": {
      if (active.kind === "group") return { kind: "invalid" };
      if (active.id === row.assetID) return { kind: "invalid" };
      return upperHalf
        ? { kind: "before-asset", assetID: row.assetID, groupID: row.groupID, depth: row.depth }
        : { kind: "after-asset", assetID: row.assetID, groupID: row.groupID, depth: row.depth };
    }
    case "group-header": {
      if (row.groupID === 0) {
        if (active.kind === "group") return { kind: "invalid" };
        return { kind: "into-group-first", groupID: 0, depth: row.depth };
      }
      if (active.kind === "group" && isGroupSelfOrDescendant(row.groupID, active.id, groups)) {
        return { kind: "invalid" };
      }
      return upperHalf
        ? { kind: "before-group", groupID: row.groupID, depth: row.depth }
        : { kind: "into-group-first", groupID: row.groupID, depth: row.depth };
    }
    case "empty-placeholder": {
      if (active.kind === "group" && isGroupSelfOrDescendant(row.groupID, active.id, groups)) {
        return { kind: "invalid" };
      }
      return { kind: "into-group-first", groupID: row.groupID, depth: row.depth };
    }
  }
}
```

- [ ] **Step 2.4: 跑测试验证通过**

```bash
cd frontend && pnpm test -- --run src/lib/assetTreeDnd/__tests__/insertionPoint.test.ts
```

Expected: 全部 passed

- [ ] **Step 2.5: Commit**

```bash
git add frontend/src/lib/assetTreeDnd/insertionPoint.ts frontend/src/lib/assetTreeDnd/__tests__/insertionPoint.test.ts
git commit -m "✨ AssetTree DnD: 插入点判定 computeInsertionPoint"
```

---

## Task 3: 翻译为后端参数 `insertionToReorderArgs`

**Files:**
- Create: `frontend/src/lib/assetTreeDnd/reorderArgs.ts`
- Create: `frontend/src/lib/assetTreeDnd/__tests__/reorderArgs.test.ts`

- [ ] **Step 3.1: 写失败测试**

写入 `frontend/src/lib/assetTreeDnd/__tests__/reorderArgs.test.ts`：

```ts
import { describe, it, expect } from "vitest";
import { insertionToReorderArgs } from "../reorderArgs";
import { asset_entity, group_entity } from "@/wailsjs/go/models";

function g(id: number, parentID = 0): group_entity.Group {
  return new group_entity.Group({ ID: id, ParentID: parentID, Status: 1 });
}
function a(id: number, groupID: number, sortOrder: number): asset_entity.Asset {
  return new asset_entity.Asset({ ID: id, GroupID: groupID, SortOrder: sortOrder, Status: 1 });
}

describe("insertionToReorderArgs - asset", () => {
  const groups = [g(1), g(2), g(11, 1)];
  const assets = [a(10, 1, 10), a(11, 1, 20), a(20, 2, 10), a(21, 2, 20)];

  it("before-asset → asset to that group, beforeID = the asset", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-asset", assetID: 11, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 11 });
  });

  it("after-asset 中间 → beforeID = next sibling", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 11 });
  });

  it("after-asset 末尾 → beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 11, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 0 });
  });

  it("after-asset 末尾时 next sibling 排除 active 自己", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 11 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 11, targetGroupID: 1, beforeID: 0 });
  });

  it("before-group(G) + asset → 移到 G.ParentID 末尾", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-group", groupID: 11, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 0 });
  });

  it("into-group-first(G) + asset, G 已有 assets → beforeID = G 首位 asset", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 2, depth: 0 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 2, beforeID: 20 });
  });

  it("into-group-first(G) + asset, G 没有 asset → beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 11, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 11, beforeID: 0 });
  });

  it("root-end + asset → targetGroupID = 0, beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "root-end" },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 0, beforeID: 0 });
  });
});

describe("insertionToReorderArgs - group", () => {
  const groups = [g(1), g(2), g(11, 1), g(12, 1)];
  const assets: asset_entity.Asset[] = [];

  it("before-group(G) + group → 同父级排到 G 之前", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-group", groupID: 2, depth: 0 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 0, beforeID: 2 });
  });

  it("into-group-first(G) + group, G 有子 group → beforeID = G 首位子 group", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 1, depth: 0 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 1, beforeID: 11 });
  });

  it("into-group-first(G) + group, G 无子 group → beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 2, depth: 0 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 2, beforeID: 0 });
  });

  it("root-end + group → targetParentID = 0, beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "root-end" },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 0, beforeID: 0 });
  });

  it("before-asset/after-asset + group → null（group 不能落在 asset 之间）", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toBeNull();
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toBeNull();
  });

  it("invalid → null", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "invalid" },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toBeNull();
  });
});
```

- [ ] **Step 3.2: 跑测试验证失败**

```bash
cd frontend && pnpm test -- --run src/lib/assetTreeDnd/__tests__/reorderArgs.test.ts
```

Expected: 全部 FAIL

- [ ] **Step 3.3: 写实现**

写入 `frontend/src/lib/assetTreeDnd/reorderArgs.ts`：

```ts
import { asset_entity, group_entity } from "@/wailsjs/go/models";
import type { InsertionPoint } from "./insertionPoint";

export type ReorderArgs =
  | { kind: "asset"; id: number; targetGroupID: number; beforeID: number }
  | { kind: "group"; id: number; targetParentID: number; beforeID: number }
  | null;

export interface InsertionToReorderArgsInput {
  point: InsertionPoint;
  active: { kind: "asset" | "group"; id: number };
  groups: group_entity.Group[];
  assets: asset_entity.Asset[];
}

export function insertionToReorderArgs({ point, active, groups, assets }: InsertionToReorderArgsInput): ReorderArgs {
  if (point.kind === "invalid") return null;

  if (active.kind === "asset") {
    return computeAssetArgs(point, active.id, groups, assets);
  }
  return computeGroupArgs(point, active.id, groups);
}

function computeAssetArgs(
  point: InsertionPoint,
  activeID: number,
  groups: group_entity.Group[],
  assets: asset_entity.Asset[]
): ReorderArgs {
  switch (point.kind) {
    case "before-asset":
      return { kind: "asset", id: activeID, targetGroupID: point.groupID, beforeID: point.assetID };
    case "after-asset": {
      const siblings = assets.filter((a) => (a.GroupID ?? 0) === point.groupID && a.ID !== activeID);
      const idx = siblings.findIndex((s) => s.ID === point.assetID);
      const beforeID = idx >= 0 ? (siblings[idx + 1]?.ID ?? 0) : 0;
      return { kind: "asset", id: activeID, targetGroupID: point.groupID, beforeID };
    }
    case "before-group": {
      const grp = groups.find((g) => g.ID === point.groupID);
      const targetGroupID = grp?.ParentID ?? 0;
      return { kind: "asset", id: activeID, targetGroupID, beforeID: 0 };
    }
    case "into-group-first": {
      const siblings = assets.filter((a) => (a.GroupID ?? 0) === point.groupID && a.ID !== activeID);
      const beforeID = siblings[0]?.ID ?? 0;
      return { kind: "asset", id: activeID, targetGroupID: point.groupID, beforeID };
    }
    case "root-end":
      return { kind: "asset", id: activeID, targetGroupID: 0, beforeID: 0 };
    case "invalid":
      return null;
  }
}

function computeGroupArgs(
  point: InsertionPoint,
  activeID: number,
  groups: group_entity.Group[]
): ReorderArgs {
  switch (point.kind) {
    case "before-asset":
    case "after-asset":
      return null;
    case "before-group": {
      const grp = groups.find((g) => g.ID === point.groupID);
      const targetParentID = grp?.ParentID ?? 0;
      return { kind: "group", id: activeID, targetParentID, beforeID: point.groupID };
    }
    case "into-group-first": {
      const targetParentID = point.groupID;
      const childGroups = groups.filter((g) => (g.ParentID ?? 0) === targetParentID && g.ID !== activeID);
      const beforeID = childGroups[0]?.ID ?? 0;
      return { kind: "group", id: activeID, targetParentID, beforeID };
    }
    case "root-end":
      return { kind: "group", id: activeID, targetParentID: 0, beforeID: 0 };
    case "invalid":
      return null;
  }
}
```

- [ ] **Step 3.4: 跑测试验证通过**

```bash
cd frontend && pnpm test -- --run src/lib/assetTreeDnd/__tests__/reorderArgs.test.ts
```

Expected: 全部 passed

- [ ] **Step 3.5: Commit**

```bash
git add frontend/src/lib/assetTreeDnd/reorderArgs.ts frontend/src/lib/assetTreeDnd/__tests__/reorderArgs.test.ts
git commit -m "✨ AssetTree DnD: 翻译插入点到 ReorderArgs"
```

---

## Task 4: 指示器 store + barrel export

**Files:**
- Create: `frontend/src/lib/assetTreeDnd/store.ts`
- Create: `frontend/src/lib/assetTreeDnd/index.ts`

- [ ] **Step 4.1: 写 store**

写入 `frontend/src/lib/assetTreeDnd/store.ts`：

```ts
import { create } from "zustand";
import type { InsertionPoint } from "./insertionPoint";

interface IndicatorState {
  point: InsertionPoint | null;
  indicatorY: number | null;
  indicatorDepth: number | null;
  setIndicator: (point: InsertionPoint | null, y: number | null, depth: number | null) => void;
  clear: () => void;
}

export const useAssetTreeDndStore = create<IndicatorState>((set) => ({
  point: null,
  indicatorY: null,
  indicatorDepth: null,
  setIndicator: (point, indicatorY, indicatorDepth) => set({ point, indicatorY, indicatorDepth }),
  clear: () => set({ point: null, indicatorY: null, indicatorDepth: null }),
}));
```

- [ ] **Step 4.2: 写 barrel**

写入 `frontend/src/lib/assetTreeDnd/index.ts`：

```ts
export { flattenTree, type Row, type FlattenInput } from "./flattenTree";
export {
  computeInsertionPoint,
  rowKey,
  type InsertionPoint,
  type RowRect,
  type ComputeInsertionPointInput,
} from "./insertionPoint";
export { insertionToReorderArgs, type ReorderArgs, type InsertionToReorderArgsInput } from "./reorderArgs";
export { useAssetTreeDndStore } from "./store";
```

- [ ] **Step 4.3: 验证 typecheck 通过**

```bash
cd frontend && pnpm exec tsc --noEmit -p tsconfig.json 2>&1 | grep -E "assetTreeDnd|error TS"
```

Expected: 无输出（无 error）

- [ ] **Step 4.4: Commit**

```bash
git add frontend/src/lib/assetTreeDnd/store.ts frontend/src/lib/assetTreeDnd/index.ts
git commit -m "✨ AssetTree DnD: indicator store + barrel"
```

---

## Task 5: 抽取 `reorderAssetsOptimistically` 到独立文件

> 这个纯函数还要留用，但旧的 `assetTreeReorder.ts` 整个要删。Task 6 改 AssetTree 时会 import 新位置，所以先抽出来。

**Files:**
- Create: `frontend/src/lib/assetTreeReorderOptimistic.ts`
- Create: `frontend/src/lib/__tests__/assetTreeReorderOptimistic.test.ts`

- [ ] **Step 5.1: 写新模块**

写入 `frontend/src/lib/assetTreeReorderOptimistic.ts`：

```ts
export interface OptimisticAsset {
  ID: number;
  GroupID?: number;
  SortOrder?: number;
}

export function reorderAssetsOptimistically<T extends OptimisticAsset>(
  assets: T[],
  movedID: number,
  targetGroupID: number,
  beforeID: number
): T[] {
  const moving = assets.find((asset) => asset.ID === movedID);
  if (!moving) return assets;

  const moved = { ...moving, GroupID: targetGroupID } as T;
  const withoutMoved = assets.filter((asset) => asset.ID !== movedID);
  const targetSiblings = withoutMoved.filter((asset) => (asset.GroupID || 0) === targetGroupID);
  const orderedTarget: T[] = [];
  let inserted = false;

  for (const asset of targetSiblings) {
    if (!inserted && beforeID !== 0 && asset.ID === beforeID) {
      orderedTarget.push(moved);
      inserted = true;
    }
    orderedTarget.push(asset);
  }
  if (!inserted) orderedTarget.push(moved);

  const normalizedTarget = orderedTarget.map((asset, index) => ({
    ...asset,
    GroupID: targetGroupID,
    SortOrder: (index + 1) * 10,
  })) as T[];

  const next: T[] = [];
  let targetBlockInserted = false;
  for (const asset of withoutMoved) {
    if ((asset.GroupID || 0) === targetGroupID) {
      if (!targetBlockInserted) {
        next.push(...normalizedTarget);
        targetBlockInserted = true;
      }
      continue;
    }
    next.push(asset);
  }

  if (!targetBlockInserted) next.push(...normalizedTarget);
  return next;
}
```

- [ ] **Step 5.2: 写测试**

写入 `frontend/src/lib/__tests__/assetTreeReorderOptimistic.test.ts`：

```ts
import { describe, expect, it } from "vitest";
import { reorderAssetsOptimistically } from "@/lib/assetTreeReorderOptimistic";

describe("reorderAssetsOptimistically", () => {
  it("moves an asset into the target folder tail immediately", () => {
    const next = reorderAssetsOptimistically(
      [
        { ID: 1, GroupID: 0, SortOrder: 10, Name: "root" },
        { ID: 2, GroupID: 10, SortOrder: 10, Name: "first" },
        { ID: 3, GroupID: 10, SortOrder: 20, Name: "last" },
      ],
      1,
      10,
      0
    );
    expect(next.filter((asset) => asset.GroupID === 10).map((asset) => asset.ID)).toEqual([2, 3, 1]);
    expect(next.find((asset) => asset.ID === 1)?.GroupID).toBe(10);
  });
});
```

- [ ] **Step 5.3: 跑测试**

```bash
cd frontend && pnpm test -- --run src/lib/__tests__/assetTreeReorderOptimistic.test.ts
```

Expected: 1 passed

- [ ] **Step 5.4: Commit**

```bash
git add frontend/src/lib/assetTreeReorderOptimistic.ts frontend/src/lib/__tests__/assetTreeReorderOptimistic.test.ts
git commit -m "♻️ 抽取 reorderAssetsOptimistically 到独立文件"
```

---

## Task 6: 改造 `AssetTree.tsx` — 替换 DnD 逻辑

**Files:**
- Modify: `frontend/src/components/layout/AssetTree.tsx`

> 整个重构最大的一步。完成后 AssetTree.tsx 不再 import `@/lib/assetTreeReorder`，为 Task 7 删除旧文件做准备。

- [ ] **Step 6.1: 改 imports**

替换原 `import {...} from "@/lib/assetTreeReorder"` 整段为：

```ts
import {
  flattenTree,
  computeInsertionPoint,
  insertionToReorderArgs,
  rowKey,
  useAssetTreeDndStore,
  type Row,
  type RowRect,
} from "@/lib/assetTreeDnd";
import { reorderAssetsOptimistically } from "@/lib/assetTreeReorderOptimistic";
```

删除 `assetTreeCollision` 函数（如果存在），删除 `pointerWithin` / `closestCenter` / `useDroppable` / `CollisionDetection` import（如果只在被删代码里用到）。保留 `DndContext, DragOverlay, PointerSensor, useSensor, useSensors, type DragStartEvent, type DragEndEvent`，新增 `type DragMoveEvent, type DragCancelEvent`。

- [ ] **Step 6.2: 删除旧的 `assetTreeCollision` 与 `useDroppable` 残留**

搜索文件内是否还有：
- `assetTreeCollision` 函数定义
- `collisionDetection={...}` 属性
- `useDroppable({ id: "group-drop-...` 调用
- `data-asset-tree-dropzone` / `data-asset-tree-drop-indicator` 属性

逐一删除。

- [ ] **Step 6.3: 重写 handleDragStart / DragMove / DragCancel / DragEnd**

在 AssetTree 函数体内，找到现有的 handleDragStart/handleDragEnd 替换为：

```ts
const hoverExpandTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
const hoverExpandTargetRef = useRef<number | null>(null);

const rowsRef = useRef<Row[]>([]);

// 把 flattenTree 的结果同步到 ref，drag callback 才能拿到最新值（不触发 re-render）
const rows = useMemo(
  () =>
    flattenTree({
      groups,
      assets: filteredAssets,
      collapsedGroupIDs: new Set(collapsedGroupIds),
      shouldHideEmpty: shouldHideEmptyGroups,
    }),
  [groups, filteredAssets, collapsedGroupIds, shouldHideEmptyGroups]
);
useEffect(() => {
  rowsRef.current = rows;
}, [rows]);

const collectRowRects = useCallback((): Map<string, RowRect> => {
  const m = new Map<string, RowRect>();
  const nodes = document.querySelectorAll<HTMLElement>("[data-asset-tree-row]");
  for (const node of nodes) {
    const key = node.getAttribute("data-asset-tree-row");
    if (!key) continue;
    const rect = node.getBoundingClientRect();
    m.set(key, { top: rect.top, bottom: rect.bottom, height: rect.height });
  }
  return m;
}, []);

const parseActive = (id: string): { kind: "asset" | "group"; id: number } | null => {
  const m = /^(asset|group)-(\d+)$/.exec(id);
  if (!m) return null;
  return { kind: m[1] as "asset" | "group", id: Number(m[2]) };
};

const clearHoverExpand = () => {
  if (hoverExpandTimerRef.current) {
    clearTimeout(hoverExpandTimerRef.current);
    hoverExpandTimerRef.current = null;
  }
  hoverExpandTargetRef.current = null;
};

const handleDragStart = (e: DragStartEvent) => {
  const active = parseActive(String(e.active.id));
  if (active?.kind === "asset") {
    setDragPreviewAsset(assets.find((asset) => asset.ID === active.id) ?? null);
  } else {
    setDragPreviewAsset(null);
  }
};

const handleDragMove = (e: DragMoveEvent) => {
  const active = parseActive(String(e.active.id));
  if (!active) return;
  const activatorEvent = e.activatorEvent as PointerEvent;
  const pointerY = activatorEvent.clientY + e.delta.y;

  const point = computeInsertionPoint({
    rows: rowsRef.current,
    rowRects: collectRowRects(),
    pointerY,
    active,
    groups,
  });

  const { y, depth } = projectIndicator(point, rowsRef.current, collectRowRects());
  useAssetTreeDndStore.getState().setIndicator(point, y, depth);

  // 折叠 group hover 500ms 自动展开
  const hoverGroupID =
    point.kind === "before-group" || point.kind === "into-group-first" ? point.groupID : null;
  const hoverRow = hoverGroupID !== null ? rowsRef.current.find(
    (r) => r.kind === "group-header" && r.groupID === hoverGroupID
  ) : null;
  if (
    hoverRow &&
    hoverRow.kind === "group-header" &&
    hoverRow.collapsed &&
    hoverExpandTargetRef.current !== hoverGroupID
  ) {
    clearHoverExpand();
    hoverExpandTargetRef.current = hoverGroupID;
    hoverExpandTimerRef.current = setTimeout(() => {
      toggleGroupCollapsed(hoverGroupID!);
      hoverExpandTimerRef.current = null;
    }, 500);
  } else if (!hoverRow || !("collapsed" in hoverRow) || !hoverRow.collapsed) {
    clearHoverExpand();
  }
};

const handleDragCancel = (_e: DragCancelEvent) => {
  setDragPreviewAsset(null);
  useAssetTreeDndStore.getState().clear();
  clearHoverExpand();
};

const handleDragEnd = async (e: DragEndEvent) => {
  setDragPreviewAsset(null);
  clearHoverExpand();
  const point = useAssetTreeDndStore.getState().point;
  useAssetTreeDndStore.getState().clear();
  if (!point) return;

  const active = parseActive(String(e.active.id));
  if (!active) return;

  const args = insertionToReorderArgs({ point, active, groups, assets });
  if (!args) return;

  let appliedOptimistic = false;
  try {
    if (args.kind === "asset") {
      await afterDragCleanupFrame();
      useAssetStore.setState((state) => ({
        assets: reorderAssetsOptimistically(state.assets, args.id, args.targetGroupID, args.beforeID),
      }));
      appliedOptimistic = true;
      await ReorderAsset(args.id, args.targetGroupID, args.beforeID);
    } else {
      await ReorderGroup(args.id, args.targetParentID, args.beforeID);
    }
    await refresh();
  } catch (err) {
    if (appliedOptimistic) void refresh();
    toast.error(String(err));
  }
};

// projectIndicator: 把 InsertionPoint 翻译为蓝线渲染坐标
function projectIndicator(
  point: ReturnType<typeof computeInsertionPoint>,
  rowsList: Row[],
  rects: Map<string, RowRect>
): { y: number | null; depth: number | null } {
  if (point.kind === "invalid" || point.kind === "root-end") {
    if (point.kind === "root-end" && rowsList.length > 0) {
      const last = rowsList[rowsList.length - 1];
      const rect = rects.get(rowKey(last));
      if (rect) return { y: rect.bottom, depth: 0 };
    }
    return { y: null, depth: null };
  }
  if (point.kind === "before-asset" || point.kind === "before-group") {
    const target =
      point.kind === "before-asset"
        ? rowsList.find((r) => r.kind === "asset" && r.assetID === point.assetID)
        : rowsList.find((r) => r.kind === "group-header" && r.groupID === point.groupID);
    if (!target) return { y: null, depth: null };
    const rect = rects.get(rowKey(target));
    if (!rect) return { y: null, depth: null };
    return { y: rect.top, depth: point.depth };
  }
  if (point.kind === "after-asset") {
    const target = rowsList.find((r) => r.kind === "asset" && r.assetID === point.assetID);
    if (!target) return { y: null, depth: null };
    const rect = rects.get(rowKey(target));
    if (!rect) return { y: null, depth: null };
    return { y: rect.bottom, depth: point.depth };
  }
  // into-group-first
  const target = rowsList.find((r) => r.kind === "group-header" && r.groupID === point.groupID);
  if (!target) return { y: null, depth: null };
  const rect = rects.get(rowKey(target));
  if (!rect) return { y: null, depth: null };
  return { y: rect.bottom, depth: point.depth + 1 };
}
```

> `projectIndicator` 移到 AssetTree.tsx 顶部（在 component 之外）。

- [ ] **Step 6.4: 改 DndContext 配置**

找到 `<DndContext ...>` 替换 props 为：

```tsx
<DndContext
  sensors={sensors}
  onDragStart={handleDragStart}
  onDragMove={handleDragMove}
  onDragCancel={handleDragCancel}
  onDragEnd={handleDragEnd}
>
```

不要传 `collisionDetection`。

- [ ] **Step 6.5: 把 `<SortableContext>` 替换为简单 div**

dnd-kit `SortableContext` 是为 sortable strategy 服务的（自动 shift 邻居）。本设计不要自动 shift。把：

```tsx
<SortableContext items={sortableIds} strategy={verticalListSortingStrategy}>
  <div className="p-2 space-y-0.5 isolate">
    ...
  </div>
</SortableContext>
```

替换为：

```tsx
<div className="p-2 space-y-0.5 isolate relative">
  ...
  <DropIndicator />
</div>
```

删除 `SortableContext`、`verticalListSortingStrategy` 的 import。

新增 `<DropIndicator />` 内联组件（放在 AssetTree.tsx 顶部）：

```tsx
function DropIndicator() {
  const y = useAssetTreeDndStore((s) => s.indicatorY);
  const depth = useAssetTreeDndStore((s) => s.indicatorDepth);
  if (y === null) return null;
  return (
    <div
      className="pointer-events-none fixed left-0 right-0 z-50 h-0.5 bg-primary"
      style={{
        top: y - 1,
        marginLeft: depth ? `${20 + depth * 12}px` : "20px",
      }}
    />
  );
}
```

- [ ] **Step 6.6: 改 AssetRow / GroupItem 不再用 sortable.transform**

定位到 GroupItem 内 `groupRowStyle` 定义，删除 `transform: CSS.Transform.toString(sortable.transform)` 和 `transition: sortable.transition`。保留 `opacity: sortable.isDragging ? 0.5 : undefined`。

同样在 AssetRow 内：删除外层 div 的 `transform` 与 `transition` 行。保留 `opacity`、`zIndex`。

给 AssetRow 的最外层 div 增加属性：

```tsx
data-asset-tree-row={`asset-${asset.ID}`}
```

给 GroupItem 的 `groupRowContent` div（line 837 附近）增加：

```tsx
data-asset-tree-row={`group-${group.ID}`}
```

给空 group 的 `+ 添加资产` 那个 div 增加：

```tsx
data-asset-tree-row={`empty-${group.ID}`}
```

可以同时删除 `CSS.Transform.toString` 的 import 如果不再用到。

- [ ] **Step 6.7: 删除现已无用的 state/refs**

- 删除 `sortableIds` useMemo 整段
- 删除 `sortable.transition` 引用

- [ ] **Step 6.8: typecheck + lint**

```bash
cd frontend && pnpm exec tsc --noEmit -p tsconfig.json && pnpm lint
```

Expected: 都通过；如果 lint 有 prettier issue 跑 `pnpm lint:fix`。

- [ ] **Step 6.9: 跑测试**

```bash
cd frontend && pnpm test -- --run
```

Expected: 全部通过（`AssetTreeContextMenu.test.tsx` 不应该挂——里面没有 DnD 逻辑）。

- [ ] **Step 6.10: Commit**

```bash
git add frontend/src/components/layout/AssetTree.tsx
git commit -m "♻️ AssetTree DnD: 切换到 insertion-point + 蓝线模型"
```

---

## Task 7: 删除旧的 DnD 模块

**Files:**
- Delete: `frontend/src/lib/assetTreeReorder.ts`
- Delete: `frontend/src/__tests__/assetTreeReorder.test.ts`

- [ ] **Step 7.1: 删除文件**

```bash
rm frontend/src/lib/assetTreeReorder.ts frontend/src/__tests__/assetTreeReorder.test.ts
```

- [ ] **Step 7.2: 全仓搜索无残留引用**

```bash
grep -rn "assetTreeReorder\"" frontend/src 2>&1
```

Expected: 无输出。如果有引用，说明上一步遗漏，需要补。

- [ ] **Step 7.3: 跑完整测试 + typecheck + lint**

```bash
cd frontend && pnpm test -- --run && pnpm exec tsc --noEmit && pnpm lint
```

Expected: 全绿。

- [ ] **Step 7.4: Commit**

```bash
git add -A frontend/src/lib/assetTreeReorder.ts frontend/src/__tests__/assetTreeReorder.test.ts
git commit -m "🔥 删除旧的 over-based DnD 模块"
```

---

## Task 8: 端到端手测

`make dev` 跑起来。挨个跑以下场景，每条记录是否通过：

- [ ] **8.1** 同组内拖某个 asset 到另一个 asset 上半区 → 落在它前面，蓝线显示在前一行底部
- [ ] **8.2** 同组内拖某个 asset 到另一个 asset 下半区 → 落在它后面
- [ ] **8.3** 跨组下拖 asset 到目标组**最后一位**的下半区 → asset 落到目标组**末尾**
- [ ] **8.4** 跨组上拖 asset 到目标组**最后一位**的下半区 → asset 落到目标组**末尾**（这条是当前 bug，重构后必须通过）
- [ ] **8.5** 拖 asset 到另一个 group header 上半区 → asset 移到父级（变成顶层未分组或父 group 的 asset）
- [ ] **8.6** 拖 asset 到另一个 group header 下半区 → asset 进入该 group 首位
- [ ] **8.7** 拖 asset 到自己当前所在组的 header 上 → 不动（无蓝线、无后端调用）
- [ ] **8.8** 拖 asset 到自己当前行上 → 不动
- [ ] **8.9** 拖 asset 到树最底部空白 → 落到未分组桶末尾
- [ ] **8.10** 拖 asset 到折叠 group header 上 → hover 500ms 后该 group 自动展开，松手时进入其首位
- [ ] **8.11** 拖 group 到另一 group header 上半区 → 同父级排到该 group 之前
- [ ] **8.12** 拖 group 到另一 group header 下半区 → 嵌套进去成为首个子 group
- [ ] **8.13** 拖 group 到自己/自己的子孙 → 无蓝线、无操作
- [ ] **8.14** 拖 group 到未分组桶 → 无蓝线、无操作
- [ ] **8.15** 拖 group 到 asset row 上 → 无蓝线、无操作
- [ ] **8.16** k3s-master-1（用户报的 bug case）：从 B 拖回 A 中间某个 asset 上 → 落在指针所在的 asset 位置，**不再跳到末尾**

每条不通过的，停止手测，回去 debug；通过则继续。

---

## Task 9: 清理意外的 transform 副作用（如果手测发现）

> 占位 task：手测中如果发现"原位 placeholder"风格的视觉反馈不可接受（比如用户实际希望看到邻居挤开），来这里加 fallback：把 sortable.transform 重新挂上、改 `collectRowRects` 用 `getBoundingClientRect()` 实时拿值（已 transform 之后）。这块在 Task 6 阶段不动，先按 spec 做"原位 placeholder"。

如果手测全过 → 跳过此 task。

---

## Final Self-Review

实施完所有 task 后，跑：

```bash
cd frontend && pnpm test -- --run && pnpm lint && pnpm exec tsc --noEmit
```

确认 all green。然后看 git log：

```bash
git log --oneline main..HEAD
```

应该有这些 commit：
- `📄 添加 AssetTree DnD 重构设计文档`
- `✨ AssetTree DnD: 扁平化函数 flattenTree`
- `✨ AssetTree DnD: 插入点判定 computeInsertionPoint`
- `✨ AssetTree DnD: 翻译插入点到 ReorderArgs`
- `✨ AssetTree DnD: indicator store + barrel`
- `♻️ 抽取 reorderAssetsOptimistically 到独立文件`
- `♻️ AssetTree DnD: 切换到 insertion-point + 蓝线模型`
- `🔥 删除旧的 over-based DnD 模块`

git status 应该 clean（除了 file-manager 等不相关的 in-progress 改动）。
