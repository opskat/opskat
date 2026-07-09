# AI 会话「绑定 tab + 联动开关双向同步」重做 — 设计规格 (issue #160, 修订 v2)

> 状态：设计定稿待评审 · 2026-07-09
> 关系：**修订并 supersede** 原稿 `2026-07-09-ai-follow-terminal-design.md` 的「跟随」语义（§4.1「绑资产」/ §4.4 / §4.5 / §6「跟随重绑」/ §7 开放问题 1）。原稿的默认收起联动资产条、Rail 头像取绑定资产、上下文 active 标记等仍有效。
> 触发：维护者复审已落地的 follow 实现，指出：① 绑的应是「此 tab」而非资产；② 跟随逻辑很奇怪，期望是**绑定后切 tab 自动切对应会话、切会话自动切对应 tab**（双向）；③ 关联资产下拉的图标/颜色没复用 `getIconComponent`/`getIconColor`；④ 右侧会话列表头像图标也不一致。

## 1. 问题：现状实现与期望不符

已落地的 follow（`aiStore.ts`）实际行为，与用户心智模型不符：

- **绑的是资产（`linkedAssetId`），不是终端也不是 tab。** 同一主机开两个终端它分不清，只认 assetId（`tabToAssetRef` 按 assetId 去重，`lib/tabAsset.ts:4`）。
- **「跟随此终端」是单向改绑**：开关打开后（`setSidebarTabFollow`, `aiStore.ts:1960`）立即绑到当前激活 tab 的资产；之后每次**切换工作区 tab**，所有开跟随的会话被**重新绑定**到新 tab 的资产 + 弹 toast（`aiStore.ts:2450` 的 tabStore 订阅）。
- **没有反向**：切换 AI 会话不会切换终端 tab（`activateSidebarTab`, `aiStore.ts:1932` 只 set `activeSidebarTabId`）。

**期望模型**：绑定建立稳定的 tab↔会话配对；开启联动后**双向导航同步**——切到该 tab → AI 面板自动切到该会话；切到该会话 → 工作区自动激活该 tab。这是**导航联动**，不是「改绑资产上下文」。

同时两处图标未复用 canonical helper（AGENTS.md「Reuse first」）：

- **`LinkedAssetControl.tsx`**（关联资产下拉）：用 `getAssetType(type).icon`（资产**类型**默认图标）+ 硬编码 `text-primary/80`，从不调 `getIconComponent`/`getIconColor`。根因：下拉的 `AssetRef` 只带 `assetType`，没带资产自己的 `Icon` 串。隔壁 `MentionList.tsx:276` 却是用 canonical helper 的——同一 AI 功能内不一致。
- **`SideAssistantTabBar.tsx`**（会话列表头像）：绑定态用类型图标 + **标题哈希调色板**（`getSessionIconColor`），颜色与资产无关——绑到同一资产的两个会话可显示不同颜色。

## 2. 决策（本次拍板）

| 决策点 | 选择 | 理由 |
|---|---|---|
| 绑定目标 | **tab 实例（严格）** | 用户明确「绑此 tab」；1 会话 ↔ 1 具体 tab |
| tab 关闭时 | **保留资产上下文，仅导航联动失效** | 误关终端不该丢掉 AI 对该资产的操作能力 |
| 绑定基数 | **1:1 独占，新绑抢走** | 双向联动无歧义，符合「一对」心智 |
| 联动开关 | **保留**，改语义 + 文案 | 绑定与联动解耦：绑定负责配对/显示/注入上下文；开关闸控是否双向联动 |
| 新会话默认 | **默认不绑 · 手动开** | 沿用原稿 issue #160 默认，不扰人 |

**绑定与联动解耦**：
- **绑定 tab**（1:1 独占）：建立会话↔tab 配对，chip 显示资产、注入 AI 上下文。**不自动切换。**
- **联动开关**（per-会话，保留控件，改语义）：开启后才启用双向导航联动；关闭时绑定照常存在，只是不自动切。开关仍 `disabled` 直到已绑定（先绑才能联动，同现有 `LinkedAssetControl.follow.test.tsx:54`）。

## 3. 数据模型（`SidebarAITab`, `aiStore.ts:160`）

```
- followActiveTerminal        // 删除（旧「自动改绑」语义）
+ syncTab?: boolean           // 新开关：是否对本会话启用双向联动
+ linkedTabId?: string | null // 绑定目标：工作区 tab 实例 id（导航联动）
  linkedAssetId / Name / Type // 保留：派生资产（AI 上下文 + 显示），tab 关闭后仍在
```

- `linkedTabId` 存工作区 tab 实例 id（终端 tab id = 后端 `sessionId`）。
- asset 三字段仍从 `tabToAssetRef(tab)`（`lib/tabAsset.ts:4`）派生并写入；tab 关闭后保留，供上下文注入与 chip 显示。
- `sanitizeSidebarTab`（`aiStore.ts:269`）/ `createSidebarTab`（`:232`）/ `didSidebarStructureChange`（`:2415`，把 `linkedTabId`/`syncTab` 计入结构变更即时落盘）同步更新。

## 4. 绑定操作（替换 `setSidebarTabAsset`/`clearSidebarTabAsset`/`setSidebarTabFollow`）

- `bindSidebarTab(sidebarTabId, workspaceTabId)`：
  1. `tabToAssetRef(workspaceTab)` 派生资产（无资产的 tab 不可绑）。
  2. **1:1 独占**：遍历 `sidebarTabs`，清掉其它 `linkedTabId === workspaceTabId` 的会话（新绑抢走）。
  3. 写入本会话 `linkedTabId` + `linkedAsset*`。
- `unbindSidebarTab(sidebarTabId)`：清 `linkedTabId` + asset 字段（同时 `syncTab` 置否）。
- `setSidebarTabSync(sidebarTabId, on)`：仅置 `syncTab`；**打开瞬间不强制跳转**（避免突兀），下次导航才生效。`disabled` 直到已绑定。

## 5. 双向联动引擎（替换 `aiStore.ts:2450` 现有订阅）

受 `syncTab` 闸控。两个方向 + 防回环：

- **方向 A（切 tab → 切会话）**：订阅 `tabStore.activeTabId`；变化时找 `linkedTabId === newActiveTabId` **且 `syncTab === true`** 的会话，存在且非当前激活 → `activateSidebarTab(it)`。
- **方向 B（切会话 → 切 tab）**：`activateSidebarTab` 内（或订阅 `activeSidebarTabId`）——目标会话 `syncTab === true` 且 `linkedTabId` 对应 tab 存在且非激活 → `tabStore.activateTab(linkedTabId)`；失效（tab 已关）则不动。
- **防回环**：两方向都「目标已激活即 no-op」，天然收敛；再加模块级 `__syncing` guard 兜底（进入同步时置位，结束清除，重入直接 return）。
- 未开联动的会话完全不受影响（保留现有 `aiStore.follow.test.ts:68` 语义，只是判据从 follow 改为 syncTab）。

## 6. 生命周期

- **tab 关闭**（`tabStore.closeTab`）：**不改数据**。渲染时用 `tabStore` 判断 `linkedTabId` 是否仍存在 → 不存在则 chip 置灰「tab 已关闭」、联动暂停，但 `linkedAssetId` 仍按 §原稿 4.4 注入 AI 上下文（`_sendForConversation`, `aiStore.ts:1604`）。重绑新 tab 即恢复。
- **重连换 id**：终端重连时 `tabStore.replaceTabId(old,new)`（`terminalStore.ts:631`）触发 `replaceHooks`（`tabStore.ts:75/205`）。注册一个 replaceHook：把匹配 `linkedTabId===old` 的会话迁移到 `new`。→ 绑定跨重启/重连存活（重启恢复时 tab 保留原持久化 id，`linkedTabId` 初始即吻合；用户重连换 id 时由 hook 迁移）。

## 7. 图标 / 颜色复用（点 ③ ④）

新增共享同步解析器（放 `lib/` 或 `components/ai/` 小工具），统一给 AI 三处用：

```
resolveAssetIcon(assetId, fallbackType) →
  const a = useAssetStore.getState().assets.find(x => x.ID === assetId)
  if (a?.Icon) return { Icon: getIconComponent(a.Icon), color: getIconColor(a.Icon) }
  return { Icon: getAssetType(fallbackType)?.icon ?? Server, color: undefined } // 资产已删才回退
```

- **`LinkedAssetControl.tsx`**：chip 图标 + 「已打开终端」列表都走它；去掉硬编码 `text-primary/80`，图标用 `getIconColor` 真实颜色。
- **`SideAssistantTabBar.tsx`**：绑定态头像用资产真实图标 + `getIconColor` 上色；未绑定态维持现有标题首字 + `getSessionIconColor` 哈希底色不变。
- 结果：下拉、会话列表、资产树/tab（`assetTree.ts:11` `renderEntityIcon`）、@提及列表（`MentionList.tsx:276`）全部同一套 `getIconComponent`/`getIconColor`。
- 数据源确认：终端 tab `meta.assetIcon` 已带图标串（`terminalStore.ts:557`）；`assetStore.assets[]` 同步可查资产 `Icon` 字段（`assetStore.ts:44`）。

## 8. 文案 / i18n（`zh-CN`/`en` `common.json`, `ai.sidebar.*`）

- 开关：`跟随此终端` → **`联动此 tab`**；状态 `跟随中` → **`联动中`**。
- 删 `followSwitched`（不再有「改绑」toast）；`followHint` 改述联动语义。
- chip 绑定态显示资产；`Link2` 图标**仅在联动开启时**点亮（区分「已绑未联动」/「已绑且联动中」）；tab 关闭态显示「tab 已关闭·重绑」。

## 9. 测试（TDD，改写现有）

- `aiStore.follow.test.ts` → 绑定+联动：
  - `bindSidebarTab` 派生资产 + 1:1 抢占（B 绑走 A 已绑的 tab → A 变未绑）。
  - 方向 A：`syncTab` 会话，切 tab → 激活对应会话；未开 syncTab 不动。
  - 方向 B：激活会话 → 激活对应 tab；`linkedTabId` 失效则不动。
  - 防回环：A→B 不无限重入。
  - tab 关闭：chip 判据置灰、`linkedAssetId` 保留并仍注入上下文。
  - replaceHook：`replaceTabId(old,new)` 迁移 `linkedTabId`。
- `LinkedAssetControl.follow.test.tsx` → 联动开关 `disabled` 直到已绑定；图标解析走 `getIconComponent`/`getIconColor`（断言用资产 Icon 而非类型默认）。
- 新增 `SideAssistantTabBar` 头像图标解析测试（绑定态取资产 Icon+色）。

## 10. 落地扩展点（供实现计划）

- `aiStore.ts`：模型字段增删（§3）；`bindSidebarTab`/`unbindSidebarTab`/`setSidebarTabSync`（§4）；双向联动订阅重写（§5）；replaceHook 注册（§6）；`_sendForConversation` 上下文注入沿用（`linkedAssetId`，`:1604`）。
- `LinkedAssetControl.tsx`：绑定/联动 UI（chip + 联动开关闸门）、图标解析器接入。
- `SideAssistantTabBar.tsx`：头像图标解析器接入。
- 新增 `resolveAssetIcon`（§7）——勿在两处各写一遍。
- 复用：`getIconComponent`/`getIconColor`（`IconPicker.tsx:606`）、`getAssetType`（`assetTypes/index.ts:5`）、`tabToAssetRef`、`replaceHooks`、`useAssetStore.assets`。
- i18n：§8。

## 11. 开放/已答

- ~~绑定目标 tab vs 资产~~ 已定：tab 实例。
- ~~tab 关闭行为~~ 已定：保留资产上下文、仅导航失效。
- ~~绑定基数~~ 已定：1:1 独占抢走。
- ~~联动开关去留~~ 已定：保留、改语义为双向联动闸门。
- ~~新会话默认~~ 已定：默认不绑·手动开。

## 12. 后续
评审通过 → `superpowers:writing-plans` 出实现计划（TDD 任务）。分支沿用 `feature/ai-follow-terminal`（off main）。
