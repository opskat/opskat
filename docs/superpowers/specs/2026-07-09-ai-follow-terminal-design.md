# AI 助手跟随终端 — 设计规格 (issue #160)

> 状态：设计定稿待评审 · 2026-07-09
> 决策：**软绑定 + 可选跟随**（方案 A 为底，折入方案 B 的跟随为开关）
> 视觉稿：`~/Desktop/opskat.pen` → frame `issue160 · AI跟随终端 · 定稿(软绑定+可选跟随)`（原方案 A，`RuT9o`）；`方案 B（备选未采纳）` 保留为纯跟随参照。

## 1. 问题 (issue #160)

AI 助手当前按聊天框一次性关联，切换终端后无法快速知道哪个 AI 对应哪个终端；且用户反馈「新会话没强调从当前远程连接查询时，AI 有时会在本地或任意终端执行」。

评论区两派诉求：
- **维护者 (CodFrm)**：纯跟随「逻辑怪怪的」，倾向在对话上加引用标签表示引用到的资产，点击可跳转（优先复用已打开 tab）。
- **用户 (xdzgithub)**：加「跟随终端」开关，AI 窗口随 SSH 窗口自动切换成组，像文件夹跟踪。
- **用户 (fr58386612)**：担心同一会话内跟随不同终端会造成上下文混乱；建议「打开 AI 即关联当前终端」。

## 2. 现状 (实际 UI，syncing baseline)

AI 助手 = 右侧 **Side Assistant** 面板（`src/components/ai/SideAssistant*`），会话状态在 `aiStore`。关键事实：

- **面板**：`SideAssistantPanel.tsx` — `bg-sidebar` / `border-panel-divider`，默认宽 360（280–520），顶部 32px Wails 拖拽条。可「提升为工作区 tab」(`type:"ai"`)。
- **Header** (`SideAssistantHeader.tsx`)：`Bot` + 标题 `ai.sidebar.title` + 4 个 ghost 图标钮 `History · ArrowUpRight(提升为tab) · Plus(新会话) · PanelRightOpen(收起)`。**无资产 chip、无模型选择器**。
- **ContextBar** (`SideAssistantContextBar.tsx`)：仅「会话标题 + ✏️重命名」一行（双击标题也可改名），空态显示 `ai.sidebar.noConversation`。**不显示任何资产上下文。**
- **会话 Rail** (`SideAssistantTabBar.tsx`)：右侧竖排，默认**折叠 36px**；顶部组 = `ChevronsRight`(rail折叠) + `Plus`；会话项 = **h-7 字母头像**（标题首字 + `sessionIconColor` 哈希取色）+ **状态点**（waiting=warning / running=info+spinner / done=success / error=destructive）；激活项 `ring-2 ring-primary`。可展开 120–220px。
- **会话模型**：`SidebarAITab { id, conversationId, title, createdAt, uiState }` — **没有 assetId**。
- **AI 如何知道资产**（今天，仅两条路径）：
  1. **显式 `@mention`**：`@` 唤起资产选择器（`MentionList.tsx`，已会读 `useTabStore.activeTabId` 偏置数据库 tab 建议），发送时内联序列化为 `<mention asset-id=... type=... host=... database=... table=...>`（`src/lib/mentionXml.ts`）。前端不再单独维护 mentions 数组。
  2. **隐式 openTabs 灌注**：每次发送 `_sendForConversation` (`aiStore.ts:1573`) 收集**全部**打开的非 AI/page tab → `AIContext.openTabs`（`TabInfo{type,assetId,assetName}`）。**无 active/主次标记**——这正是「AI 有时在错误终端执行」的根因。
- **激活终端来源**：`useTabStore.activeTabId → Tab.meta`（`TerminalTabMeta`/`QueryTabMeta` 带 `assetId/assetName/assetType`）。已连接集合 = `useActiveAssetIds()`。

## 3. 决策：默认不绑定 · 按需绑定 · 可选跟随

**默认未绑定，且上下文条默认收起**——多数会话不需要绑定，顶部保持干净（只标题 + `关联资产 ⌄` 入口）。想聚焦某资产时，用户**手动或 @mention** 建立绑定（主资产，在上下文标 active，修「跑错终端」）。绑定之上再有一层**默认隐藏 / 关的「跟随」开关**（开 = 随激活终端切换重绑）。即：不强加、按需、渐进——绑定与跟随都是显式 opt-in。

对比取舍：
| 方案 | 满足 | 风险 | 结论 |
|---|---|---|---|
| A 纯软绑定+引用标签 | 维护者直觉、fr58386612 | 不满足要自动切换的用户 | 作底 |
| B 纯跟随成组（每终端一会话） | xdzgithub | 会话生命周期绑终端，重；与提升为tab/多会话 rail 冲突 | 备选未采纳 |
| **A + 可选跟随（选定）** | 三方 | 跟随内混上下文的风险变为**默认关闭的显式选项** | ✅ |

## 4. 设计规格

### 4.1 会话的「主资产绑定」（**默认无**）
`SidebarAITab` 增加 `linkedAssetId` + 冗余 `linkedAssetType/linkedAssetName/linkedAssetIcon`（供 chip 渲染，避免每次查 assetStore）。**默认未绑定**（`linkedAssetId` 为空）——新会话**不自动绑定**，AI 上下文维持现状（`AIContext.openTabs` = 全部 tab）。绑定由用户建立：(a) **手动**——展开上下文 → 点 `绑定资产` / chip `▾` 从已打开或资产库选择；(b) **@mention**——未绑定会话首次 `@` 某资产时，自动 set 为 `linkedAssetId`（可再改 / 清除）。

### 4.2 联动资产条（升级 ContextBar，**默认收起**）
现 `SideAssistantContextBar`（仅标题）升级为**可折叠**条。**默认收起**——只显示标题行，绑定与引用都藏起来（默认未绑定、多数会话不需要，不占视觉）：
- **标题行（始终可见）**：`[会话标题（fill，截断） ✏️] …… [关联资产 ⌄]`
  - 标题 `fill_container` 截断（生产用 CSS `truncate` 省略号）；双击 / ✏️ 重命名（沿用现 `renameConversation`）。**标题与绑定不同行。**
  - **`关联资产 ⌄` 展开入口**：克制的 ghost 开关（收起 `⌄` / 展开 `⌃`）；点开才显示下方上下文区。已绑定时可在入口加小圆点提示（可选）。
- **关联资产区（默认收起，展开后才显示）**：
  - **绑定行**：未绑定 = `[⊝ 绑定资产 ▾]`（ghost 入口）；已绑定 = `[● 图标 prod-web-01 ▾]`。`▾` 菜单（`AssetSelect` 式）：已打开终端 / 会话置顶（✓，偏置 open tabs 同 `MentionList`）· `从资产库选择…`（全量）· `清除绑定` ·（分隔线）· `🔗 跟随此终端`（默认关）。
  - **本次引用行（有引用时）**：`本次引用 [cache ↗] [prod-web-02 +]`，`↗` = 复用已开 tab / `+` = 新开；来源 = `@mention` + 工具调用命中的资产。
- **🔗 跟随**：藏在绑定 chip 的 `▾` 菜单、默认关；开后 chip **前缀 `🔗`**，切换激活终端时本会话主资产重绑（仅本会话，不新开会话）。

> **考虑过的替代（未采纳）**：把关联资产做成**输入框上下文 pills**（Cursor / Copilot 式——主资产 = 输入框上方置顶 pill、`@`/`+` 添加、引用显示在回答气泡下 `用到 …`）。默认更干净、上下文就在打字处、贴现代 AI 习惯；但选择 **B 头部折叠**——集中在一处、"本会话关于 X"在顶部一目了然。对比稿见 `opskat.pen` frame `issue160 · AI跟随终端 · 方案对比 A vs B`。

### 4.3 Rail 头像反映绑定资产（优化，回应 issue 核心痛点）
「切换终端后不知道哪个 AI 对应哪个终端」→ Rail 每个会话头像由**绑定资产**派生（首字/图标 + 会话取色 + 状态点），一眼区分 `AI-for-prod-web-01 / AI-for-cache`。保留现有 `ring-2 ring-primary` 激活态与状态点语义。

### 4.4 上下文正确性（关键后端联动，低成本高收益）
在 `AIContext` 中把**绑定的主资产标为 active**（`TabInfo` 增 `active:boolean`，或 `AIContext` 增 `primaryAssetId`）。使 AI 优先落到绑定终端——直接消除「在本地/错误终端执行」。开跟随时该标记随激活 tab 更新。

### 4.5 边界态
- 打开 AI 时无激活资产 tab → chip = `未绑定 · 选择资产`（点开选择器）。
- 绑定的 tab 被关闭/断连 → `●` 变灰，chip 仍保留（仍是上下文），可 `▾` 重绑。
- 提升为工作区 tab（`type:"ai"`）→ 联动资产条与跟随开关随之呈现，行为一致。

### 4.6 交互状态（已在 `opskat.pen` 画出）
frame `issue160 · AI跟随终端 · 交互状态`（8 张卡）：
1. **切换 / 绑定主资产**：点 chip `▾` → `AssetSelect` 式下拉——「已打开的终端 / 会话」置顶（当前项带 ✓，偏置 open tabs 同 `MentionList`），分隔线，`从资产库选择…`（全量），`清除绑定`，末尾 `🔗 跟随此终端` 开关（默认关）。
2. **跟随态**：默认隐藏（顶部只显示 chip）vs 跟随中（chip 前缀 `🔗`）；开关本身在 `▾` 菜单内、默认关。
3. **跟随中切换终端**：激活 tab 改变 → chip 自动重绑 + 轻提示 `↻ 已跟随切换到 …`，输入框上下文同步。
4. **展开 · 未绑定（默认起始态）**：展开上下文后未绑定 → `[⊝ 绑定资产 ▾]`。
5. **本次引用点击跳转**：`↗` = 复用已打开 tab / `+` = 新开 tab；hover tooltip 说明；来源 = `@mention` + 工具调用命中资产。
6. **会话轨悬停**：头像派生自绑定资产（首字 + 取色 + 状态点）；hover 显示所属终端与运行状态。
7. **默认收起 vs 展开**：默认只显示标题行 + `关联资产 ⌄`；展开 `⌃` 后才显示绑定行 + 本次引用行。
8. **@mention 自动绑定**：未绑定会话首次 `@资产` → 自动 set 为主资产（chip 出现，可在 `▾` 菜单改绑 / 清除）。

## 5. 与视觉稿同步修正 (mock → 实际 UI)

| 处 | 原稿 | 已改为（同步实际） |
|---|---|---|
| Header 图标 | `sparkles/square-pen/maximize-2/panel-right-close` | `bot / history / arrow-up-right(提升) / plus(新) / panel-right-open` |
| 标题/重命名 | 原稿两帧都丢了 | 标题行始终可见（标题截断 + `关联资产 ⌄` 入口）；绑定 / 引用在**默认收起**的上下文区，跟随收进 `▾` 菜单 |
| Rail | 通用圆角图标钮，40px | 36px + 顶部 `chevrons-right/plus` + **字母头像 + 状态点**（激活 ring） |
| @mention | 气泡内联 chip | 保留（与实际内联 `<mention>` 一致） |

调色沿用 `.pen` 既有硬编码深色约定（与 etcd/oss 帧一致），未改用运行时 oklch token——运行时 token 见 §2/`globals.css`，落地时用真 token。

## 6. 落地扩展点（供实现计划）

- `aiStore.ts`：`SidebarAITab` 加 `linkedAssetId` 等；`openNewSidebarTab` 读 `tabStore.activeTabId` 自动绑定；`_sendForConversation`(`:1573`) 组装 `AIContext` 时标记 active 主资产；跟随开态订阅 `useTabStore` 的 `activeTabId` 变化重绑。
- 后端绑定：`AIContext`/`TabInfo`（`frontend/wailsjs/go/models.ts` + Go 侧 `runner`）加 `active`/`primaryAssetId`；Prompt 侧优先主资产。
- 组件：`SideAssistantContextBar.tsx` 升级为联动资产条（复用 `AssetSelect` 做 `▾` 选择器）；`SideAssistantTabBar.tsx` 头像取绑定资产；跟随开关放联动资产条（**不**塞进已满的 Header）。
- 复用：`AssetSelect`、`useActiveAssetIds()`、资产类型图标解析（`getIconComponent`/`getAssetType`），勿另起。
- i18n：`ai.sidebar.*` 增 `linkedAsset/follow/followOn/followOff/unbound/referencedThisSession` 等（en/zh）。

## 7. 开放问题

1. **跟随语义**：定稿=开关重绑**当前会话**主资产（轻）。是否要 xdzgithub 的「每终端一会话自动切换成组」（重，会话绑终端生命周期）？当前作为备选未采纳。
2. **引用标签来源**：仅 `@mention`，还是也纳入 AI 工具调用实际命中的资产？后者更准但需后端回传命中资产列表。
3. 主资产 chip 的 `▾` 是否允许绑定「非当前打开」的资产（全量 AssetSelect），还是仅在已打开 tab 间选择？
4. 跟随开关默认值：定稿=**默认隐藏（收进 `▾` 菜单）且关**（跟随非高频）。是否某些资产类型/场景默认开？
5. ~~跟随控件形态 / 位置~~ **已定**：跟随**收进 chip `▾` 菜单、默认隐藏**（非高频，不常驻顶部）；跟随中时 chip 前缀 `🔗`。标题与 chip 同行，标题截断避免遮挡。

## 8. 后续
评审通过 → `superpowers:writing-plans` 出实现计划（TDD 任务）。落地应在独立分支 `feature/ai-follow-terminal`（off main），勿混入当前 `feature/oss-asset-type`。
