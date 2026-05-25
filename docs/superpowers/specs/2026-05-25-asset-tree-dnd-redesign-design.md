# Asset Tree DnD 重构设计

**日期**：2026-05-25
**范围**：`frontend/src/components/layout/AssetTree.tsx` 内 asset + group 的拖拽排序逻辑

## 背景

当前 AssetTree 的拖拽实现依赖 dnd-kit 的 `closestCenter` collision detection，把"用户落在哪个 droppable 上"作为唯一语义信号，再用 splice-projection 算法 (`getAssetTreeMoveBeforeId`) 反推 `beforeID`。经多轮排查（见对话历史）已确认这条架构在多个边界场景上无法稳定工作：

1. `closestCenter` 在 group 与 group 之间的窄过渡区把 over 选成源组自己的 header → asset 留在源组末尾（已用 `getAssetTreeTargetContainerId` 拦截，部分缓解）
2. `closestCenter` 在 active rect 中心更靠近其它 group header 时，把 over 选成相邻 group 的 header → asset 被错误追加到该 group 末尾（已用 `pointerWithin` fallback closestCenter 缓解，但 dnd-kit 的"active rect 中心"语义本身就不等于"用户视觉上落点"）
3. splice-projection 算法只能映射出"插到 over 之前"的 beforeID；上拖到目标组最后一位永远落到倒数第二（已用方向感知 `getAssetTreeAfterBeforeId` 缓解，但和 collision 修复叠加后仍有边角问题）
4. 整套无可见落点指示——用户看不到"会落在哪里"，是上述所有现象的体验放大器

补丁式修复已经三轮，每次都暴露新边角。结论：**架构本身不适合当前需求**，需要换模型。

## 新模型：Insertion-Point + 蓝线指示器

借鉴 VS Code Explorer / Notion / macOS Finder：

1. 把整棵树**扁平化**为一列 Row（每个 group header / asset row / "+ Add Asset" 占位 各一个 Row）
2. 每两个相邻 Row 之间是一个**插入点**；group header 内部上下半区也是不同的插入点（上半 = 该组之前，下半 = 进入该组首位）
3. **dragMove 时**：用 pointer Y vs 每个 Row 的 rect，确定当前插入点；写入一个 store
4. **渲染**：一条 absolute-positioned 横向蓝线 (`<DropIndicator>`) 订阅 store，定位到当前插入点的 Y 坐标
5. **dragEnd 时**：读 store 的当前插入点，翻译成 `(targetContainerID, beforeID)`，调 `ReorderAsset` / `ReorderGroup`

`closestCenter`、`pointerWithin`、`over.id` 这些 dnd-kit 内置 collision 概念全部不用。dnd-kit 仅用来做"哪个 row 在被拖（active）"和"拖动中的 pointer 位置"。

## 落点语义矩阵（pointer Y 所在区域 → 含义）

| 区域 | active = asset | active = group |
|---|---|---|
| asset row 上半区 | 插到该 asset 之前（同组内同位） | 无效（asset 行不接受 group 拖拽）|
| asset row 下半区 | 插到该 asset 之后（同组）；该 asset 是组末尾 → 落在组末尾 | 无效 |
| group header 上半区 | 插到该 group 之前（父级）—— asset 出现在该 group 之上、父级同位 | 插到该 group 之前（同父级）|
| group header 下半区 | 进入该 group 作为**首位** asset | 进入该 group 作为**首位**子 group（嵌套）|
| 折叠 group header 下半区 | 触发 500ms hover 自动展开后再判定 | 同上 |
| 空 group 的 "+ Add Asset" 占位 | 进入该 group 作为首位 asset | 进入该 group 作为首位子 group |
| 树最底部空白 | 落到根的末尾（未分组桶之后，若桶存在）| 落到根的末尾（顶层 group 列表尾）|
| 拖到自己 / 自己的子孙 | 无效（不出现指示器，dragEnd 不调用后端）| 同左 |
| 未分组桶 group header | 进入未分组（asset 出离原 group）| 无效（group 不能嵌入未分组桶）|

**关键设计取舍**：没有专用的"追加到末尾"落点（不再有 tail dropzone）。要把 asset 放到 group G 末尾 → 拖到 G 内最后一个 row 的下半区。这与 Notion / VS Code 一致，避免重新引入隐藏 droppable。

## 模块拆分

```
frontend/src/lib/assetTreeDnd/
├── flattenTree.ts        // Tree → Row[]
├── insertionPoint.ts     // (Row[], pointerY, rowRects, dragKind) → InsertionPoint
├── reorderArgs.ts        // InsertionPoint → ReorderArgs (调后端用)
├── store.ts              // useDndIndicatorStore (Zustand, 仅 indicator 状态)
├── index.ts              // re-exports
└── __tests__/
    ├── flattenTree.test.ts
    ├── insertionPoint.test.ts
    └── reorderArgs.test.ts
```

### `flattenTree.ts`

```ts
type Row =
  | { kind: "group-header"; groupID: number; depth: number; collapsed: boolean }
  | { kind: "asset"; assetID: number; groupID: number; depth: number }
  | { kind: "empty-placeholder"; groupID: number; depth: number };

function flattenTree(input: {
  groups: Group[];
  assets: Asset[];
  collapsedGroupIDs: Set<number>;
  shouldHideEmpty: boolean;
}): Row[]
```

DFS 顺序：每个 group 先 push header，若 expanded 则递归 push 子 group、再 push 该 group 的直接 assets，若该 group 既无 children 也无 assets 则 push empty-placeholder。最后处理未分组桶（group 0，仅当有 root assets 时）。

### `insertionPoint.ts`

```ts
type InsertionPoint =
  | { kind: "before-group"; groupID: number }           // 插到该 group 之前（同父级）
  | { kind: "into-group-first"; groupID: number }       // 进入该 group 作首位
  | { kind: "before-asset"; assetID: number }           // 插到该 asset 之前
  | { kind: "after-asset"; assetID: number }            // 插到该 asset 之后
  | { kind: "root-end" }                                 // 树末尾
  | { kind: "invalid" };                                 // 落到自己 / 自己的子孙 / 不允许的目标

function computeInsertionPoint(input: {
  rows: Row[];
  rowRects: Map<string, DOMRect>;   // key = `${kind}-${id}`
  pointerY: number;
  active: { kind: "asset" | "group"; id: number };
  groups: Group[];                  // 用来检查 group 子孙关系（拖 group 不能落到自己子孙下）
}): InsertionPoint
```

算法：
1. 找出 pointer Y 落在哪个 Row 的 rect 内（线性扫描，N 通常 < 1000）
2. 算出在该 Row 的 rect 内是上半区 (`Y < top + height/2`) 还是下半区
3. 按上面的语义矩阵映射成 InsertionPoint
4. 校验：active.kind 与该位置是否兼容；active 是 group 时再校验目标不是自己或自己的子孙 → 不兼容时返回 `invalid`

### `reorderArgs.ts`

```ts
type AssetReorderArgs = { kind: "asset"; id: number; targetGroupID: number; beforeID: number };
type GroupReorderArgs = { kind: "group"; id: number; targetParentID: number; beforeID: number };
type ReorderArgs = AssetReorderArgs | GroupReorderArgs | null;

function insertionToReorderArgs(input: {
  point: InsertionPoint;
  active: { kind: "asset" | "group"; id: number };
  rows: Row[];          // 用来在 "after-asset/before-asset" 时找到 next sibling
}): ReorderArgs
```

返回 `null` 时不调用后端（落点无效或拖到原位）。

### `store.ts`

```ts
interface IndicatorState {
  point: InsertionPoint | null;
  indicatorY: number | null;        // 蓝线渲染坐标
  indicatorDepth: number | null;    // 缩进
  setPoint: (p: InsertionPoint | null, y: number | null, d: number | null) => void;
  clear: () => void;
}
```

只在 dragStart/dragMove/dragEnd 时被 AssetTree 的事件回调更新；`<DropIndicator>` 订阅它定位。

## dnd-kit 用法

### DndContext

```tsx
<DndContext
  sensors={sensors}
  onDragStart={handleDragStart}
  onDragMove={handleDragMove}
  onDragCancel={handleDragCancel}
  onDragEnd={handleDragEnd}
>
  {/* 不再传 collisionDetection */}
  {/* 不再用 SortableContext 的 sortable strategy，但仍用 useSortable 拿 draggable handle */}
  ...
</DndContext>
```

### 行组件

`AssetRow` / `GroupRow` 继续用 `useSortable({ id })` 拿 `attributes`、`listeners`、`setNodeRef`。**不应用 `sortable.transform`** —— 不再让 dnd-kit 自动 shift 其它 row（自动 shift 会让 rowRects 与视觉不一致，破坏 pointer-vs-rect 计算）。Active row 用半透明 placeholder 留在原位即可；拖动视觉由 `<DragOverlay>` 处理（保持现有）。

### handleDragMove

```ts
function handleDragMove(e: DragMoveEvent) {
  const pointerY = e.activatorEvent.clientY + e.delta.y;
  const rows = currentRowsRef.current;
  const rowRects = collectRowRects();   // 从 setNodeRef 收集
  const point = computeInsertionPoint({ rows, rowRects, pointerY, active: parsedActive, groups });
  const { y, depth } = projectIndicator(point, rowRects);
  useDndIndicatorStore.getState().setPoint(point, y, depth);
}
```

### handleDragEnd

```ts
function handleDragEnd(e: DragEndEvent) {
  const point = useDndIndicatorStore.getState().point;
  useDndIndicatorStore.getState().clear();
  if (!point) return;

  const args = insertionToReorderArgs({ point, active: parsedActive, rows: currentRowsRef.current });
  if (!args) return;

  // 乐观更新（保留 reorderAssetsOptimistically）+ 调后端 + refresh
  // group 同理（保留现有 group reorder optimistic 路径）
}
```

### 折叠 group 自动展开

`handleDragMove` 检测 hover 在折叠 group header 上时，启动 `setTimeout(expand, 500)`；hover 离开或 dragEnd → clear timer。展开后 `flattenTree` 会被 `useMemo` 自动重算，rowRects 在下一帧 measure 完成。

## 删除清单

- `frontend/src/lib/assetTreeReorder.ts` 整文件删除
- 对应 `frontend/src/__tests__/assetTreeReorder.test.ts` 整文件删除
- AssetTree.tsx 内：
  - `assetTreeCollision`、`pointerWithin` 引用、`closestCenter` 引用
  - `useDroppable({ id: "group-drop-${group.ID}" })` 整段及 `contentDrop.setNodeRef`、`contentDrop.isOver`
  - `handleDragEnd` 内基于 over 的 target/beforeID 计算路径
  - `data-asset-tree-dropzone` / `data-asset-tree-drop-indicator` 属性
- `frontend/src/__tests__/AssetTreeContextMenu.test.tsx` 内已无 tail dropzone 断言（前轮已删），无需再动

## 保留

- `reorderAssetsOptimistically`：纯函数，依然适用，不动
- 后端 `ReorderAsset` / `ReorderGroup` / `asset_svc.Reorder` / `group_svc.Reorder` / `sortutil.ReorderSiblings`：行为不变
- `useSortable` 的 attributes/listeners/setNodeRef：仍是 row 的拖拽 handle 与 rect 测量来源
- `DragOverlay` + `AssetDragPreview`：拖动视觉表现保留

## 测试

`flattenTree.test.ts`：
1. 多级嵌套 group + 各级 assets，验证 Row 顺序与 depth
2. 折叠某个 group → 该 group 的子项不出现
3. 未分组桶在末尾 push
4. 空 group 出现 empty-placeholder
5. shouldHideEmpty=true → 空 group 整个不出现

`insertionPoint.test.ts`（每条 case 一段已扁平化的 rows + 一组 rowRects + pointerY）：
1. 上半区 asset row → before-asset
2. 下半区 asset row（中间） → after-asset
3. 下半区 asset row（组末尾 asset） → after-asset（reorderArgs 阶段翻译成 beforeID=0）
4. 上半区 group header → before-group
5. 下半区 group header → into-group-first
6. 折叠 group header → 返回 into-group-first（500ms 展开行为由 AssetTree 层负责）
7. pointer 超出树底部 → root-end
8. active 是 group A，pointer 在 A 自己的子孙 group header 上 → invalid
9. active 是 group，pointer 在 asset row 上 → invalid
10. active 是 asset，pointer 在自己当前 row 上 → invalid

`reorderArgs.test.ts`：
1. before-asset → `{ kind: "asset", targetGroupID = 该 asset 的 groupID, beforeID = 该 assetID }`
2. after-asset 中间 → `{ targetGroupID = 同组, beforeID = next sibling ID }`
3. after-asset 末尾 → `{ targetGroupID = 同组, beforeID = 0 }`
4. before-group（root 级） → `{ kind: "group", targetParentID = 0, beforeID = 该 groupID }`
5. into-group-first（有 assets） → `{ kind: "asset", targetGroupID = 该 group, beforeID = 该 group 首位 asset ID }`
6. into-group-first（空 group） → `{ kind: "asset", targetGroupID = 该 group, beforeID = 0 }`
7. into-group-first（active 是 group） → `{ kind: "group", targetParentID = 该 group, beforeID = 该 group 首位子 group ID 或 0 }`
8. root-end → `{ targetParentID/GroupID = 0, beforeID = 0 }`
9. invalid → `null`
10. active 已经在目标位置（如 before-asset 的 asset 紧跟在 active 之后） → `null`（避免触发无意义重排）

可选 + 后续做：组件级测试用 `@dnd-kit/core` 的 `KeyboardSensor` 模拟键盘拖拽 + jsdom DOMRect mock。纯函数覆盖率够的话首版不做。

## 实施次序

1. 创建 `assetTreeDnd/` 三个纯函数 + 测试（一次性写完，绿了再继续）
2. 创建 `store.ts` + `<DropIndicator>` 组件
3. 改 AssetTree.tsx：注入 store、新 `handleDragMove`、新 `handleDragEnd`；保留旧路径直到新路径跑通
4. 删除 `assetTreeReorder.ts`、`assetTreeReorder.test.ts`、AssetTree 内 over-based 逻辑、tail dropzone 残留
5. 接上 500ms 折叠 group 自动展开
6. 端到端手测：所有语义矩阵列出的场景 + bug 复现路径

## 风险与回滚

- 风险：`useSortable` 不应用 transform 后，asset rows 拖动时不再自动 shift（视觉上 active row 保留半透明 placeholder）。需要确认这对 UX 可接受。如果不可接受，可以恢复 transform，并在 `collectRowRects` 阶段读 `getBoundingClientRect()` 实时 rect（包含 transform 之后位置）—— 但这样 pointer-vs-rect 又变成移动靶。
- 回滚：完整保留旧实现的 commit 在 git history，必要时 revert 整个重构 commit。
