# AI 对话绑定「打开的标签页」（收窄 v2）设计

> **Supersedes** the "从资产库选择" pick path in `2026-07-09-ai-tab-binding-sync-design.md`. 其余 v2 模型（`linkedTabId` + `syncTab` 双向联动引擎、`resolveAssetIcon` 图标复用、1:1 独占绑定）不变。

**Goal:** 把 AI 侧栏对话的绑定对象从「资产」彻底收窄为「当前打开的标签页」——删除「从资产库选择」入口、绑定列表按 tab 逐个列出、所有 `资产` 文案改为 `标签页`。

**Issue / PR:** #160 / PR #224（`feature/ai-follow-terminal`）。

## 背景与动机

v2 已把绑定改为「绑 tab 实例 + 联动开关」，但 `LinkedAssetControl` 下拉里仍保留一条「从资产库选择」——它绑的是任意资产（`workspaceTabId: null`），与「绑定打开的标签页」的心智模型冲突。维护者要求：这里只应绑「打开的标签页」，删掉资产库入口，并把 `关联资产` 等文案统一改为 `标签页`。

心智模型定稿：**AI 对话绑定的是一个打开的工作区标签页**。绑定的标签页背后的资产仍用于显示（图标+名）与喂给 AI 上下文，但用户不再从资产库任选资产。

## 决策（已与维护者确认）

1. **文案范围 = 全部 `资产 → 标签页`**，含外层可展开的 `关联资产` 区标题（`contextSection`/`contextExpand`/`contextCollapse`）。接受该区内仍含「引用资产」列表带来的轻微语义出入。
2. **列表粒度 = 按 tab 逐个列出**（不再按资产去重）。同一资产多开时靠 `tab.label`（tab 标题）区分——与主标签栏一致，若标题重名则沿用主标签栏既有的重名表现，不额外消歧。

## 组件改动：`frontend/src/components/ai/LinkedAssetControl.tsx`

**删除：**
- `menu-pick-library` 下拉项、`handleLibraryPick`、`<AssetSelect>` 组件与其 import、`picking` state（`useState`）。
- `pickFromLibrary` 文案引用。

**列表改造（`openAssets` → `openTabs`）：**
- 不再按 `assetId` 去重。遍历 `tabs`，凡 `tabToAssetRef(wt) != null` 的都产出一项：
  `{ tabId: wt.id, label: wt.label, assetId, assetName, assetType }`。
- 列表项展示 `tab.label`（tab 标题）+ 资产图标（`resolveAssetIcon(assets, assetId, assetType)`）+ 绿点；`isBound = tabId === tab?.linkedTabId` 时显示 ✓ 并高亮。
- `data-testid` 由 `menu-terminal-${assetId}` 改为 `menu-tab-${tab.id}`（per-tab 唯一，避免同资产多开时 testid 冲突）。
- 点击项仍走 `bindTab(sidebarTabId, { workspaceTabId: tabId, assetId, assetName, assetType })`。

**空态：**
- `openTabs.length === 0` 时，在「已打开的标签页」区显示一条 disabled 行「暂无打开的标签页」（新文案 `linkedAsset.noOpenTabs`），使菜单不为空。

**保持不变：**
- chip 触发器（绑定/未绑定两态、`resolveAssetIcon`、绿点/灰点、`Link2` 联动标记）。
- 「清除绑定」`menu-clear`、联动开关行 `menu-sync`（`disabled={!tabLive}`）、底部 hint（`tabClosed` / `syncHint`）。

## 文案改动：`frontend/src/i18n/locales/{zh-CN,en}/common.json`

`ai.sidebar` 下：

| key | zh-CN 新值 | en 新值 |
|---|---|---|
| `contextSection` | 关联标签页 | Linked tabs |
| `contextExpand` | 展开关联标签页 | Show linked tabs |
| `contextCollapse` | 收起关联标签页 | Hide linked tabs |
| `linkedAsset.pickPlaceholder` | 选择要绑定的标签页 | Choose a tab to bind |
| `linkedAsset.openTabs`（原 `openAssets`，改键名） | 已打开的标签页 | Open tabs |
| `linkedAsset.noOpenTabs`（新增） | 暂无打开的标签页 | No open tabs |
| `linkedAsset.pickFromLibrary` | **删除** | **删除** |
| `attachedAssets`（死键，代码无引用） | **删除** | **删除** |

不动：`linkedAsset.clearBinding`、`sync`、`syncing`、`linkedAsset.syncHint`、`linkedAsset.tabClosed`（已是 tab 语义）。

## 不在范围

- store（`bindSidebarTab` / `setSidebarTabSync` / 双向联动引擎 / `registerTabReplaceHook`）——不改。per-tab 仅是组件层去掉去重，`bindSidebarTab` 的 `workspaceTabId` 契约不变。
- `@mention` 自动绑定（`aiStore.ts:2271`，仍 `workspaceTabId: null` 的资产级上下文绑定）——保留。它是「无 tab 的资产级上下文」这一合法状态的唯一剩余生产者。
- 已知小瑕：`@mention` 绑出的「无 tab 资产」态会复用 `tabClosed`（"绑定的 tab 已关闭"）提示，对该分支措辞略偏；但主用途（绑 tab 后关掉该 tab）措辞正确，本次不动。

## 测试（TDD）

仅两个文件受影响：`LinkedAssetControl.test.tsx`、`LinkedAssetControl.binding.test.tsx`。

- 断言下拉里**不存在** `menu-pick-library`，且触发库选择器（`linked-asset-picker`）的路径已移除。
- 造**两个指向同一资产**的打开 tab（不同 `id`、可同名 `label`），断言列表**列出两项**（`menu-tab-<id1>`、`menu-tab-<id2>`），而非去重后一项。
- 点击 `menu-tab-<id>` → `bindSidebarTab` 收到对应 `workspaceTabId`。
- 空态：无可绑 tab 时渲染 `noOpenTabs` 文案、无标签页列表项。
- 文案 key 全部走新键（`openTabs`/`noOpenTabs`/`contextSection`=关联标签页）。

## 验证

`pnpm test src/components/ai/__tests__/LinkedAssetControl*.test.tsx` 全绿；改动文件 prettier/eslint 干净（仓库 `pnpm lint` 非门禁，只看本次改动文件）。桌面 GUI 无法被 agent 无头点击，功能以测试 + 结构断言为准。
