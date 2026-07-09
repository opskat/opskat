# AI 跟随终端 — Plan 2（后端上下文正确性 + 跟随开关 + 引用跳转 + 会话轨头像）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 兑现设计规格里 Plan 1 未覆盖的 4 件事：把绑定的主资产在后端 prompt 中**标为 active**（真正修「跑错终端」，Plan 1 仅靠 openTabs 排序近似）；给会话加**默认关的「🔗 跟随」开关**（开=随激活终端重绑）；上下文区加**本次引用行 + 点击跳转**（复用已开 tab / 新开）；**会话轨头像**改由绑定资产派生。

**Architecture:** 后端在 `runner.TabInfo` 加 `Active bool`，`buildTabContext()` 对 active tab 渲染 PRIMARY 标记；`wails generate module` 重生成 `frontend/wailsjs`（gitignore 生成物，不提交）；前端 `_sendForConversation` 给置顶的绑定 tab 设 `active:true`。跟随：`SidebarAITab` 加 `followActiveTerminal`，`setSidebarTabFollow` action（开启即绑当前激活终端），模块级 `useTabStore.subscribe` 在 `activeTabId` 变化时重绑所有 follow-on 会话。引用：从 App.tsx 抽出 `openAssetConnection(asset)` 复用 helper，`deriveReferences`/`jumpToAsset` 纯函数，`ReferencesRow` 挂进已折叠的联动资产区。头像：`SideAssistantTabBar` 用 `getAssetType(linkedAssetType)?.icon` 取图标，回退标题首字。

**Tech Stack:** 后端 Go 1.26（`internal/ai/runner`，goconvey 测试）+ `wails generate module`。前端 React 19 + TS strict + Zustand（`useAIStore`/`useTabStore`/`useTerminalStore`/`useQueryStore`/`useAssetStore`）+ react-i18next + Vitest + @testing-library/react + `@opskat/ui`（`DropdownMenu`/`AssetSelect`/`Tooltip`）。

## Global Constraints

- **前端**：命令用 **pnpm**（`pnpm exec vitest` / `pnpm exec tsc --noEmit` / `pnpm test`）。测试框架 **Vitest**。
- **ENV 锁文件陷阱**：本机任何 `pnpm exec …` 会触发 workspace install，重写 **`frontend/pnpm-lock.yaml` 与 `frontend/pnpm-workspace.yaml` 两个文件**。每次跑完从 worktree 根 **`git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml`**；提交只显式 `git add` 目标文件，**绝不 `git add -A`**。
- **wailsjs 是 gitignore 生成物**：改 Go 结构体后 `wails generate module` 重生成 `frontend/wailsjs`（`.gitignore:7`）。**不提交 wailsjs**（`git add` 时不带它）。本 worktree 已从主检出拷入一份 wailsjs 供编译；改 `TabInfo` 后必须重生成或手动补 `active` 字段，否则 TS 编译失败。
- **后端**：`go test ./internal/ai/runner/...` 验单测；**`golangci-lint run`（非 `go vet`）** 验 lint（出处 memory `Wails binding/CI flow`）。
- **i18n 双语必填**：新文案同加 `frontend/src/i18n/locales/en/common.json` 与 `zh-CN/common.json`，各语言用**地道表达**，不逐字对齐；`src/__tests__/i18n.test.ts` 校验两侧 key 集合一致。
- **Toast**：成功走 `notify.ts`（`notifySuccess` 顶部居中）；**信息/警告/错误**用 `toast.info`/`toast.warning`/`toast.error`（右下角默认位）。跟随切换是**信息提示 → `toast.info`**。不直接 `toast.success`。
- **OCP**：不在共享代码里 `if (assetType === "ssh")` 分支；资产类型从 `Tab.meta`/`MentionAttrs.type`/`linkedAssetType` 读，图标经 `getAssetType(type)?.icon` 注册表解析。
- **Reuse first**：跳转打开资产复用抽出的 `openAssetConnection`（勿再抄 App.tsx 的 dispatch）；选择器复用 `AssetSelect`；菜单复用 `@opskat/ui` 的 `DropdownMenu`。
- 提交用 **gitmoji** 前缀；TDD 微提交 subject **不带** `#160`。
- 每步跑前端测试前确保 `frontend/wailsjs` 含 `active` 字段；每步提交前 `git checkout --` 两个锁文件。

## File Structure

| 文件 | 责任 | 动作 |
|---|---|---|
| `internal/ai/runner/prompt_builder.go` | `TabInfo.Active` 字段；`buildTabContext()` 对 active tab 渲染 PRIMARY 标记 | Modify |
| `internal/ai/runner/prompt_builder_test.go` | active 标记的 goconvey 用例 | Modify |
| `frontend/wailsjs/go/models.ts` | 重生成后含 `TabInfo.active`（**不提交**） | Regen |
| `frontend/src/stores/aiStore.ts` | `_sendForConversation` 设 `active:true`；`SidebarAITab.followActiveTerminal`；`setSidebarTabFollow`；跟随重绑订阅；sanitize/persist | Modify |
| `frontend/src/lib/tabAsset.ts` | 纯函数 `tabToAssetRef(tab)`（Tab→{assetId,assetName,assetType}）| Create |
| `frontend/src/lib/openAsset.ts` | 从 App.tsx 抽出的 `openAssetConnection(asset)` 复用 dispatcher | Create |
| `frontend/src/App.tsx` | `handleConnectAsset` 改调 `openAssetConnection` | Modify |
| `frontend/src/lib/aiReferences.ts` | `deriveReferences(messages, opts)` + `jumpToAsset(assetId)` | Create |
| `frontend/src/components/ai/LinkedAssetControl.tsx` | 绑定态 chip 升级为 `▾` DropdownMenu（换绑/清除/🔗跟随）+ 🔗 前缀 | Modify |
| `frontend/src/components/ai/ReferencesRow.tsx` | 本次引用行（↗ 复用 / + 新开） | Create |
| `frontend/src/components/ai/SideAssistantContextBar.tsx` | 展开区挂 `ReferencesRow` | Modify |
| `frontend/src/components/ai/SideAssistantTabBar.tsx` | 头像取 `linkedAssetType` 图标，回退首字 | Modify |
| `frontend/src/i18n/locales/{en,zh-CN}/common.json` | `ai.sidebar.follow*` / `referencedThisSession` / `linkedAsset.change/clear` 复用 | Modify |
| 各 `__tests__/*` | 对应单测 | Create/Modify |

**范围内明确不做（留后续）**：xdzgithub 的「每终端一会话自动成组」（重，会话绑终端生命周期，spec 开放问题 1 备选未采纳）；引用来源纳入 **AI 工具调用实际命中资产**（spec 开放问题 2，需后端回传命中列表）——本计划引用仅取 **@mention**。

---

### Task 1: 后端 `TabInfo.Active` + prompt PRIMARY 标记

**Files:**
- Modify: `internal/ai/runner/prompt_builder.go`（`TabInfo` struct `:10-14`；`buildTabContext()` `:100-122`）
- Test: `internal/ai/runner/prompt_builder_test.go`（`TestPromptBuilderBuild` 内追加 Convey）

**Interfaces:**
- Produces（Go）：`TabInfo` 增字段 `Active bool \`json:"active"\``
- 行为：`buildTabContext()` 对 `tab.Active == true` 的行追加 PRIMARY 说明；其余行不变。

- [ ] **Step 1: 写失败 Go 测试**

在 `prompt_builder_test.go` 的 `TestPromptBuilderBuild` 内新增一个 `Convey`（紧邻现有 tab-context 断言）：

```go
Convey("primary/active tab is marked in tab context", func() {
	ctx := AIContext{OpenTabs: []TabInfo{
		{Type: "ssh", AssetID: 1, AssetName: "prod-web-01", Active: true},
		{Type: "ssh", AssetID: 2, AssetName: "other", Active: false},
	}}
	out := NewPromptBuilder("en", ctx).Build()
	So(out, ShouldContainSubstring, "prod-web-01")
	// active 行带 PRIMARY 提示；非 active 行不带
	So(out, ShouldContainSubstring, "PRIMARY")
	primaryLineIdx := strings.Index(out, "prod-web-01")
	otherLineIdx := strings.Index(out, "\"other\"")
	So(primaryLineIdx, ShouldBeGreaterThanOrEqualTo, 0)
	So(otherLineIdx, ShouldBeGreaterThanOrEqualTo, 0)
	// PRIMARY 只应出现在 active 行附近，不在 other 行
	So(out[otherLineIdx:], ShouldNotContainSubstring, "PRIMARY")
})
```

（若文件未 import `strings`，加上。沿用文件现有 goconvey 风格 `. "github.com/smartystreets/goconvey/convey"`。）

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/ai/runner/ -run TestPromptBuilderBuild -v 2>&1 | tail -20
```
Expected: FAIL — 输出无 `PRIMARY`（字段未渲染）。

- [ ] **Step 3: 实现**

改 `TabInfo`（`:10-14`）加字段：

```go
// TabInfo 当前打开的 Tab 信息
type TabInfo struct {
	Type      string `json:"type"` // "ssh" | "database" | "redis" | "sftp"
	AssetID   int64  `json:"assetId"`
	AssetName string `json:"assetName"`
	Active    bool   `json:"active"` // true = 会话绑定的主资产，AI 优先落到此终端
}
```

改 `buildTabContext()`（`:100-122`）的循环体，给 active 行追加标记：

```go
	for _, tab := range b.context.OpenTabs {
		typeName := tab.Type
		switch tab.Type {
		case "ssh":
			typeName = "SSH Terminal"
		case "database":
			typeName = "Database Query"
		case "redis":
			typeName = "Redis"
		case "sftp":
			typeName = "SFTP"
		}
		line := fmt.Sprintf("- %s: \"%s\" (ID: %d)", typeName, tab.AssetName, tab.AssetID)
		if tab.Active {
			line += " — PRIMARY: this is the asset this conversation is bound to; prefer it when the target is ambiguous"
		}
		lines = append(lines, line)
	}
```

- [ ] **Step 4: 跑测试确认通过 + lint**

```bash
go test ./internal/ai/runner/ -run TestPromptBuilderBuild -v 2>&1 | tail -10
golangci-lint run ./internal/ai/runner/... 2>&1 | tail -20
```
Expected: PASS；lint 0 issue（新代码）。

- [ ] **Step 5: 提交**（仅 Go 文件）

```bash
git add internal/ai/runner/prompt_builder.go internal/ai/runner/prompt_builder_test.go
git commit -m "✨ AIContext TabInfo 增加 active 主资产标记并在 prompt 中强调"
```

---

### Task 2: 重生成 wailsjs + 前端置顶 tab 设 `active:true`

**Files:**
- Regen（不提交）: `frontend/wailsjs/go/models.ts`
- Modify: `frontend/src/stores/aiStore.ts`（`_sendForConversation` openTabs 组装 `:1583-1615`）
- Test: `frontend/src/__tests__/aiStore.linkedAsset.test.ts`（追加）

**Interfaces:**
- Consumes: Task 1 的 `TabInfo.active`（生成后 TS 侧为 `active: boolean`）
- 行为：`_sendForConversation` 里置顶的绑定 `TabInfo` 设 `active: true`；其余 tab 不设（默认 `false`）。

- [ ] **Step 1: 重生成 wailsjs 绑定**

```bash
cd /Users/codfrm/Code/opskat/opskat/.claude/worktrees/ai-follow-terminal
wails generate module 2>&1 | tail -5
grep -n "active" frontend/wailsjs/go/models.ts | grep -i tabinfo -A1 || grep -n "this.active" frontend/wailsjs/go/models.ts | head
```
Expected: `frontend/wailsjs/go/models.ts` 的 `TabInfo` 类新增 `active: boolean` 与 `this.active = source["active"];`。
**若 `wails generate module` 在本环境失败**（工具链/构建问题）：手动给**（gitignore 的）** `frontend/wailsjs/go/models.ts` 的 `TabInfo` 补字段——类体加 `active: boolean;`、构造函数加 `this.active = source["active"];`。此文件不提交，手改仅为本地 TS 编译。

- [ ] **Step 2: 写失败测试**（追加到 `aiStore.linkedAsset.test.ts` 的 `_sendForConversation includes linked asset` describe 内）

```ts
it("marks the prepended bound asset active and others inactive", async () => {
  vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
  useTabStore.setState({
    tabs: [{ id: "t1", type: "terminal", label: "web", meta: { assetId: 1, assetName: "web", assetType: "ssh" } } as any],
    activeTabId: "t1",
  });
  await useAIStore.getState().sendFromSidebarTab("s1", "hi");
  const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
  const aiContext = call?.[2] as { openTabs: Array<{ assetId: number; active?: boolean }> };
  expect(aiContext.openTabs[0].assetId).toBe(99);
  expect(aiContext.openTabs[0].active).toBe(true);
  expect(aiContext.openTabs.slice(1).every((t) => !t.active)).toBe(true);
});
```
（该 describe 的 `beforeEach` 已设 s1 绑定 asset 99 type redis——沿用。）

- [ ] **Step 3: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — `openTabs[0].active` 为 `undefined`。

- [ ] **Step 4: 实现**

在 `_sendForConversation` 绑定资产置顶块（`:1601-1613`）里，给置顶的 `TabInfo` 加 `active: true`：

```ts
  const boundTab = useAIStore.getState().sidebarTabs.find((tab) => tab.conversationId === convId);
  if (boundTab?.linkedAssetId != null) {
    const rest = openTabs.filter((t) => t.assetId !== boundTab.linkedAssetId);
    openTabs.length = 0;
    openTabs.push(
      new runner.TabInfo({
        type: boundTab.linkedAssetType || "",
        assetId: boundTab.linkedAssetId,
        assetName: boundTab.linkedAssetName || "",
        active: true,
      }),
      ...rest
    );
  }
```

（`rest` 里的 tab 由前面 `.map` 构造，未设 `active`→ 生成的 `TabInfo` 默认 `active:false`。）

- [ ] **Step 5: 跑测试确认通过 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: 全绿；tsc 0 error（依赖 Step 1 已补 `active`）。

- [ ] **Step 6: 提交**（仅前端源码，**不含 wailsjs**）

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.linkedAsset.test.ts
git commit -m "✨ AI 上下文置顶主资产标记 active:true"
```

---

### Task 3: `followActiveTerminal` 字段 + `setSidebarTabFollow`（开启即绑当前激活）

**Files:**
- Create: `frontend/src/lib/tabAsset.ts`
- Modify: `frontend/src/stores/aiStore.ts`（`SidebarAITab` `:158-167`；`createSidebarTab`/`sanitizeSidebarTab`；`AIState` 动作声明；return object；`didSidebarStructureChange`）
- Test: `frontend/src/lib/__tests__/tabAsset.test.ts`、`frontend/src/__tests__/aiStore.follow.test.ts`（新建）

**Interfaces:**
- Produces（`tabAsset.ts`）：`export function tabToAssetRef(tab: Tab): { assetId: number; assetName: string; assetType: string } | null`（非资产 tab→null；terminal→type `"ssh"`；query→`meta.assetType`）
- Produces（store）：`SidebarAITab.followActiveTerminal?: boolean`；`setSidebarTabFollow(tabId: string, on: boolean): void`（`on=true` 时若当前激活 tab 有资产则立即 `setSidebarTabAsset`）

- [ ] **Step 1: 写 `tabToAssetRef` 失败测试**

Create `frontend/src/lib/__tests__/tabAsset.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { tabToAssetRef } from "../tabAsset";

describe("tabToAssetRef", () => {
  it("maps a terminal tab to an ssh asset ref", () => {
    expect(tabToAssetRef({ id: "t", type: "terminal", label: "web", meta: { assetId: 1, assetName: "web" } } as any)).toEqual({
      assetId: 1, assetName: "web", assetType: "ssh",
    });
  });
  it("maps a query tab using its meta.assetType", () => {
    expect(tabToAssetRef({ id: "q", type: "query", label: "db", meta: { assetId: 2, assetName: "db", assetType: "redis" } } as any)).toEqual({
      assetId: 2, assetName: "db", assetType: "redis",
    });
  });
  it("returns null for ai/page tabs", () => {
    expect(tabToAssetRef({ id: "a", type: "ai", label: "AI", meta: {} } as any)).toBeNull();
    expect(tabToAssetRef({ id: "p", type: "page", label: "P", meta: { type: "page" } } as any)).toBeNull();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/tabAsset.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 模块不存在。

- [ ] **Step 3: 实现 `tabToAssetRef`**

Create `frontend/src/lib/tabAsset.ts`:

```ts
import type { Tab, QueryTabMeta } from "@/stores/tabStore";

/** 从一个工作区 Tab 提取资产引用；非资产 tab（ai/page/info 或无 assetId）返回 null。 */
export function tabToAssetRef(tab: Tab): { assetId: number; assetName: string; assetType: string } | null {
  if (tab.type === "ai" || tab.type === "page" || tab.type === "info") return null;
  const meta = tab.meta as { assetId?: number; assetName?: string };
  if (meta == null || typeof meta.assetId !== "number") return null;
  const assetType = tab.type === "query" ? (tab.meta as QueryTabMeta).assetType : tab.type === "terminal" ? "ssh" : tab.type;
  return { assetId: meta.assetId, assetName: meta.assetName || tab.label || "", assetType };
}
```

（若 `Tab`/`QueryTabMeta` 未从 `tabStore` 导出，先在 `tabStore.ts` 加 `export`——它们已是 `export interface`，确认即可。）

- [ ] **Step 4: `tabToAssetRef` 测试通过**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/tabAsset.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: PASS（3 tests）。

- [ ] **Step 5: 写 store 失败测试**

Create `frontend/src/__tests__/aiStore.follow.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";
import { useTabStore } from "../stores/tabStore";

const mkTab = (over: Partial<{ id: string; conversationId: number; followActiveTerminal: boolean; linkedAssetId: number }>) => ({
  id: over.id ?? "s1",
  conversationId: over.conversationId ?? 1,
  title: "t",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  followActiveTerminal: over.followActiveTerminal,
  linkedAssetId: over.linkedAssetId,
});

describe("setSidebarTabFollow", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({
      tabs: [{ id: "t1", type: "terminal", label: "web", meta: { assetId: 5, assetName: "web" } } as any],
      activeTabId: "t1",
    });
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1" }) as any], activeSidebarTabId: "s1" });
  });

  it("enabling follow binds to the current active terminal immediately", () => {
    useAIStore.getState().setSidebarTabFollow("s1", true);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.followActiveTerminal).toBe(true);
    expect(tab?.linkedAssetId).toBe(5);
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("disabling follow keeps the binding but clears the flag", () => {
    useAIStore.getState().setSidebarTabFollow("s1", true);
    useAIStore.getState().setSidebarTabFollow("s1", false);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.followActiveTerminal).toBe(false);
    expect(tab?.linkedAssetId).toBe(5);
  });
});
```

- [ ] **Step 6: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.follow.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — `setSidebarTabFollow is not a function`。

- [ ] **Step 7: 实现**

`aiStore.ts` 顶部 import：

```ts
import { tabToAssetRef } from "@/lib/tabAsset";
```

`SidebarAITab`（`:158-167`）加字段：

```ts
export interface SidebarAITab {
  id: string;
  conversationId: number | null;
  title: string;
  createdAt: number;
  uiState: SidebarTabUIState;
  linkedAssetId?: number | null;
  linkedAssetName?: string;
  linkedAssetType?: string;
  followActiveTerminal?: boolean;
}
```

`createSidebarTab` 透传（在现有 return 里加一行）：`followActiveTerminal: overrides?.followActiveTerminal,`。
`sanitizeSidebarTab` round-trip（在现有 `createSidebarTab({...})` 参数里加）：`followActiveTerminal: typeof tab.followActiveTerminal === "boolean" ? tab.followActiveTerminal : undefined,`。

`AIState` 接口加声明（紧邻 `setSidebarTabAsset`）：

```ts
  setSidebarTabFollow: (tabId: string, on: boolean) => void;
```

store return object 加实现（紧邻 `setSidebarTabAsset`）：

```ts
    setSidebarTabFollow: (tabId, on) => {
      set((state) => ({
        sidebarTabs: state.sidebarTabs.map((tab) =>
          tab.id === tabId ? { ...tab, followActiveTerminal: on } : tab
        ),
      }));
      if (on) {
        // 开启跟随即绑定到当前激活终端（若有资产）
        const active = useTabStore.getState();
        const activeTab = active.tabs.find((t) => t.id === active.activeTabId);
        const ref = activeTab ? tabToAssetRef(activeTab) : null;
        if (ref) get().setSidebarTabAsset(tabId, ref);
      }
    },
```

`didSidebarStructureChange` 比较加 `followActiveTerminal`（让开关落盘）：

```ts
    if (
      a.id !== b.id ||
      a.conversationId !== b.conversationId ||
      a.title !== b.title ||
      a.linkedAssetId !== b.linkedAssetId ||
      a.followActiveTerminal !== b.followActiveTerminal
    )
      return true;
```

- [ ] **Step 8: 跑测试确认通过 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.follow.test.ts src/lib/__tests__/tabAsset.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: PASS（2 + 3）；tsc 0 error。

- [ ] **Step 9: 提交**

```bash
git add frontend/src/lib/tabAsset.ts frontend/src/lib/__tests__/tabAsset.test.ts frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.follow.test.ts
git commit -m "✨ 会话跟随开关字段 + setSidebarTabFollow（开启即绑当前终端）"
```

---

### Task 4: 跟随重绑订阅（切换激活终端 → 重绑 follow-on 会话）

**Files:**
- Modify: `frontend/src/stores/aiStore.ts`（模块级订阅，靠近文件末尾 `useAIStore.subscribe` 持久化处）
- Modify: `frontend/src/i18n/locales/{en,zh-CN}/common.json`
- Test: `frontend/src/__tests__/aiStore.follow.test.ts`（追加）

**Interfaces:**
- Consumes: `tabToAssetRef`（T3）、`setSidebarTabAsset`（Plan 1）、`toast`、`i18n`
- 行为：模块级 `useTabStore.subscribe`——当 `activeTabId` 变化且新激活 tab 有资产引用时，对每个 `followActiveTerminal===true` 且当前 `linkedAssetId !== 新资产` 的 sidebarTab 调 `setSidebarTabAsset` 重绑，并 `toast.info(t("ai.sidebar.followSwitched", { name }))`。

- [ ] **Step 1: 写失败测试**（追加到 `aiStore.follow.test.ts`）

```ts
describe("follow re-binds on active terminal switch", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({
      tabs: [
        { id: "t1", type: "terminal", label: "web", meta: { assetId: 5, assetName: "web" } } as any,
        { id: "t2", type: "terminal", label: "cache", meta: { assetId: 9, assetName: "cache" } } as any,
      ],
      activeTabId: "t1",
    });
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", followActiveTerminal: true, linkedAssetId: 5 }) as any],
      activeSidebarTabId: "s1",
    });
  });

  it("rebinds a follow-on conversation when the active tab changes", () => {
    useTabStore.setState({ activeTabId: "t2" }); // 触发订阅
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(9);
    expect(tab?.linkedAssetName).toBe("cache");
  });

  it("does not rebind a conversation without follow", () => {
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1", followActiveTerminal: false, linkedAssetId: 5 }) as any] });
    useTabStore.setState({ activeTabId: "t2" });
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(5);
  });

  it("ignores switches to a tab without an asset", () => {
    useTabStore.setState({ tabs: [...useTabStore.getState().tabs, { id: "p1", type: "page", label: "P", meta: { type: "page" } } as any] });
    useTabStore.setState({ activeTabId: "p1" });
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(5); // 保留上次
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.follow.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 切换 activeTabId 未重绑。

- [ ] **Step 3: 加 i18n**

`en/common.json` `ai.sidebar` 内加：`"followSwitched": "Following → {{name}}",`（并在 T5 一并加 `follow`/`following` 标签，见 T5）。
`zh-CN/common.json` `ai.sidebar` 内加：`"followSwitched": "已跟随切换到 {{name}}",`。

- [ ] **Step 4: 实现订阅**

`aiStore.ts` 顶部确保 import：`import { toast } from "sonner";` 与 `import i18n from "@/i18n";`（若已存在则复用；文件已用 i18n 做默认标题，多半已 import）。在文件末尾 `useAIStore.subscribe(...)` 持久化块**之后**加模块级订阅：

```ts
// 跟随开关：激活终端变化时，重绑所有开启跟随的会话到新激活资产。
let __lastActiveTabId: string | null = useTabStore.getState().activeTabId;
useTabStore.subscribe((state) => {
  if (state.activeTabId === __lastActiveTabId) return;
  __lastActiveTabId = state.activeTabId;
  const activeTab = state.tabs.find((t) => t.id === state.activeTabId);
  const ref = activeTab ? tabToAssetRef(activeTab) : null;
  if (!ref) return;
  const store = useAIStore.getState();
  for (const tab of store.sidebarTabs) {
    if (tab.followActiveTerminal === true && tab.linkedAssetId !== ref.assetId) {
      store.setSidebarTabAsset(tab.id, ref);
      toast.info(i18n.t("ai.sidebar.followSwitched", { name: ref.assetName }));
    }
  }
});
```

（`tabToAssetRef` 已在 T3 import。`toast.info` 走右下角默认位——信息提示，符合约定。）

- [ ] **Step 5: 跑测试确认通过 + i18n 对齐 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.follow.test.ts src/__tests__/i18n.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: 全绿；i18n key 集合一致；tsc 0 error。
> 注：`toast`/`i18n` 在测试里为真实模块——`toast.info` 在 happy-dom 下无副作用；`i18n` 已被顶部 `vi.mock("../i18n")` 替身（返回 key/fallback），断言不依赖文案。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.follow.test.ts frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "✨ 跟随开关：切换激活终端时重绑会话主资产 + 轻提示"
```

---

### Task 5: `LinkedAssetControl` chip 升级为 `▾` 菜单（换绑/清除/🔗跟随）

**Files:**
- Modify: `frontend/src/components/ai/LinkedAssetControl.tsx`
- Modify: `frontend/src/i18n/locales/{en,zh-CN}/common.json`
- Test: `frontend/src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx`（新建）

**Interfaces:**
- Consumes: `useAIStore`（`setSidebarTabFollow`）、`@opskat/ui` `DropdownMenu*`、`ai.sidebar.follow`/`following`
- 行为：**已绑定态**把原「换绑按钮 + 清除按钮」收进 `DropdownMenu`：触发器 = chip（名字前，跟随中显示 `🔗`）+ `▾`；菜单项 = `换绑`（`data-testid="menu-change"`，展开 `AssetSelect`）· `清除`（`menu-clear`）· 分隔 · `🔗 跟随此终端`（`DropdownMenuCheckboxItem`，`menu-follow`，`checked=followActiveTerminal`，`onCheckedChange→setSidebarTabFollow`）。未绑定态不变（`未绑定` + `AssetSelect`）。

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: () => <div data-testid="asset-select" />,
}));

describe("LinkedAssetControl follow menu", () => {
  beforeEach(() => {
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null }, linkedAssetId: 42, linkedAssetName: "prod-web-01", linkedAssetType: "ssh" }],
      activeSidebarTabId: "s1",
    });
  });

  it("toggles follow via the dropdown menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    fireEvent.click(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-follow"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.followActiveTerminal).toBe(true);
  });

  it("clears the binding from the menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    fireEvent.click(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-clear"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBeUndefined();
  });
});
```
> Radix `DropdownMenu` 在 jsdom/happy-dom 下点击触发器后内容才挂载；若测试环境下 `DropdownMenuContent` 未渲染，用 `DropdownMenu` 的 `open`/`defaultOpen` 受控或在测试里断言触发器存在后直接调用 store——**实现时优先保证测试能点到菜单项**（可给 `DropdownMenu` 传 `modal={false}` 并确保 content 渲染）。若确实无法在测试环境展开 Radix 菜单，改为把菜单项 onSelect 逻辑抽到可单测的 handler 并测 handler（在报告里说明所选方案）。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 无 `linked-asset-menu-trigger`。

- [ ] **Step 3: 加 i18n**

`en/common.json` `ai.sidebar` 内加：`"follow": "Follow this terminal",` `"following": "Following",`。
`zh-CN/common.json` `ai.sidebar` 内加：`"follow": "跟随此终端",` `"following": "跟随中",`。

- [ ] **Step 4: 实现**

改 `LinkedAssetControl.tsx` 的**已绑定态**（当前 `linked-asset-chip` 那段），用 `DropdownMenu` 包裹 chip 触发器：

```tsx
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuSeparator, DropdownMenuCheckboxItem,
} from "@opskat/ui";
import { ChevronDown, Link2 } from "lucide-react";
// ...
const setFollow = useAIStore((s) => s.setSidebarTabFollow);
// ...
if (tab?.linkedAssetId != null) {
  return (
    <div className="flex items-center gap-2" data-testid="linked-asset-chip">
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <button
            data-testid="linked-asset-menu-trigger"
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-secondary px-2 py-0.5 text-xs"
          >
            {tab.followActiveTerminal ? (
              <Link2 className="h-3 w-3 text-primary" />
            ) : (
              <span className="h-1.5 w-1.5 rounded-full bg-success" />
            )}
            <span className="truncate max-w-[140px]">{tab.linkedAssetName}</span>
            <ChevronDown className="h-3 w-3 opacity-60" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-[180px]" onCloseAutoFocus={(e) => e.preventDefault()}>
          <DropdownMenuItem data-testid="menu-change" onSelect={() => setPicking(true)}>
            {t("ai.sidebar.linkedAsset.change")}
          </DropdownMenuItem>
          <DropdownMenuItem data-testid="menu-clear" onSelect={() => clearAsset(sidebarTabId)}>
            {t("ai.sidebar.linkedAsset.clear")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuCheckboxItem
            data-testid="menu-follow"
            checked={!!tab.followActiveTerminal}
            onCheckedChange={(v) => setFollow(sidebarTabId, !!v)}
          >
            {t("ai.sidebar.follow")}
          </DropdownMenuCheckboxItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {picking && (
        <AssetSelect value={tab.linkedAssetId} onValueChange={handlePick} placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")} testId="linked-asset-picker" />
      )}
    </div>
  );
}
```

（保留原有 `handlePick`/`clearAsset`/`picking` 逻辑与未绑定态。`change` 走 `picking` 展开 `AssetSelect`；`clear` 走 `clearSidebarTabAsset`。删除原来独立的换绑/清除 `Button`——它们迁进菜单。）

- [ ] **Step 5: 跑测试确认通过 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx src/components/ai/__tests__/LinkedAssetControl.test.tsx src/__tests__/i18n.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: 全绿（含原 T6 测试仍通过——若原测试断言了旧的 `linked-asset-clear` 按钮 testid，同步更新为 `menu-clear` 路径或保留兼容；实现时对齐）。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/components/ai/LinkedAssetControl.tsx frontend/src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "✨ 绑定 chip 升级为 ▾ 菜单（换绑/清除/🔗跟随）"
```

---

### Task 6: 抽取 `openAssetConnection(asset)` 复用 helper

**Files:**
- Create: `frontend/src/lib/openAsset.ts`
- Modify: `frontend/src/App.tsx`（`handleConnectAsset` 改调 helper）
- Test: `frontend/src/lib/__tests__/openAsset.test.ts`

**Interfaces:**
- Produces: `export async function openAssetConnection(asset: asset_entity.Asset): Promise<void>`（复刻 App.tsx `handleConnectAsset` 的 dispatch：k8s→page tab；`connectAction==="query"`→`openQueryTab`；扩展资产→扩展 page tab；`connectAction==="terminal"`→`useTerminalStore.getState().connect(asset)`）
- Consumes: `useTabStore`/`useQueryStore`/`useExtensionStore`/`useTerminalStore`、`getAssetType`、`toast`

- [ ] **Step 1: 写失败测试**

Create `frontend/src/lib/__tests__/openAsset.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from "vitest";

const openQueryTab = vi.fn();
const connect = vi.fn().mockResolvedValue("conn-1");
const openTab = vi.fn();
const activateTab = vi.fn();

vi.mock("@/stores/queryStore", () => ({ useQueryStore: { getState: () => ({ openQueryTab }) } }));
vi.mock("@/stores/terminalStore", () => ({ useTerminalStore: { getState: () => ({ connect }) } }));
vi.mock("@/stores/tabStore", () => ({ useTabStore: { getState: () => ({ tabs: [], openTab, activateTab }) } }));
vi.mock("@/stores/extensionStore", () => ({ useExtensionStore: { getState: () => ({ getExtensionForAssetType: () => undefined }) } }));

import { openAssetConnection } from "../openAsset";

describe("openAssetConnection", () => {
  beforeEach(() => vi.clearAllMocks());

  it("opens a query tab for a database asset", async () => {
    await openAssetConnection({ ID: 1, Name: "db", Type: "mysql" } as any);
    expect(openQueryTab).toHaveBeenCalledTimes(1);
  });

  it("connects a terminal for an ssh asset", async () => {
    await openAssetConnection({ ID: 2, Name: "web", Type: "ssh" } as any);
    expect(connect).toHaveBeenCalledTimes(1);
  });
});
```
> 实现前核对 `getAssetType("mysql")?.connectAction === "query"` 与 `getAssetType("ssh")?.connectAction === "terminal"`（读 `frontend/src/lib/assetTypes/`）；若类型串不同，改用实际存在的类型（如 `database` 归属的具体驱动名），保证测试走对分支。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/openAsset.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 模块不存在。

- [ ] **Step 3: 实现 helper（从 App.tsx 平移）**

Create `frontend/src/lib/openAsset.ts`——把 `App.tsx:268-319` `handleConnectAsset` 主体搬入，`connect(asset)` 换成 `useTerminalStore.getState().connect(asset)`：

```ts
import { toast } from "sonner";
import { asset_entity } from "../../wailsjs/go/models";
import { useTabStore } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { useExtensionStore } from "@/stores/extensionStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { getAssetType } from "@/lib/assetTypes";

/** 按资产类型打开/聚焦连接 tab（k8s/扩展→page；query→查询 tab；terminal→连接）。与资产列表双击行为一致。 */
export async function openAssetConnection(asset: asset_entity.Asset): Promise<void> {
  if (asset.Type === "k8s") {
    const pageId = `k8s-${asset.ID}`;
    const tabStore = useTabStore.getState();
    if (tabStore.tabs.find((t) => t.id === pageId)) tabStore.activateTab(pageId);
    else tabStore.openTab({ id: pageId, type: "page", label: asset.Name, icon: asset.Icon || "kubernetes", meta: { type: "page", pageId: "k8s-cluster", assetId: asset.ID } });
    return;
  }
  const def = getAssetType(asset.Type);
  if (def?.connectAction === "query") {
    useQueryStore.getState().openQueryTab(asset);
    return;
  }
  const ext = useExtensionStore.getState().getExtensionForAssetType(asset.Type);
  if (ext) {
    const connectPage = ext.manifest.frontend?.pages.find((p) => p.slot === "asset.connect");
    if (connectPage) {
      useTabStore.getState().openTab({ id: `ext-${asset.ID}-${connectPage.id}`, type: "page", label: asset.Name, icon: ext.manifest.icon, meta: { type: "page", pageId: connectPage.id, extensionName: ext.name, assetId: asset.ID } });
      return;
    }
  }
  if (def?.connectAction !== "terminal") return;
  try {
    await useTerminalStore.getState().connect(asset);
  } catch (e) {
    toast.error(`${asset.Name}: ${String(e)}`);
  }
}
```

改 `App.tsx`：`handleConnectAsset` 体替换为 `await openAssetConnection(asset);`（保留其签名与调用点不变；顶部 import `openAssetConnection`；若 `connect`/`useTerminalStore` 在 App.tsx 中已无其他用途可留作它用，勿顺手删无关引用）。

- [ ] **Step 4: 跑测试确认通过 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/openAsset.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: PASS（2）；tsc 0 error。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/lib/openAsset.ts frontend/src/lib/__tests__/openAsset.test.ts frontend/src/App.tsx
git commit -m "♻️ 抽取 openAssetConnection 复用资产连接 dispatch"
```

---

### Task 7: `deriveReferences` + `jumpToAsset` 引用 helper

**Files:**
- Create: `frontend/src/lib/aiReferences.ts`
- Test: `frontend/src/lib/__tests__/aiReferences.test.ts`

**Interfaces:**
- Produces: `export function deriveReferences(messages: { role: string; content: string }[], opts?: { excludeAssetId?: number | null }): MentionAttrs[]`（遍历 `role==="user"`，`extractMentions` 汇总，按 `assetId` 去重、保序，排除 `excludeAssetId`）
- Produces: `export async function jumpToAsset(assetId: number): Promise<void>`（已开 tab→`activateTab`；否则取 `useAssetStore` 资产→`openAssetConnection`）
- Produces: `export function isAssetTabOpen(assetId: number): boolean`（供组件决定 ↗/+ 图标）

- [ ] **Step 1: 写失败测试**

Create `frontend/src/lib/__tests__/aiReferences.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from "vitest";
import { buildMentionXml } from "../mentionXml";

const activateTab = vi.fn();
const openAssetConnection = vi.fn().mockResolvedValue(undefined);
vi.mock("@/stores/tabStore", () => ({
  useTabStore: { getState: () => ({ tabs: [{ id: "t1", type: "terminal", label: "web", meta: { assetId: 5 } }], activateTab }) },
}));
vi.mock("@/stores/assetStore", () => ({
  useAssetStore: { getState: () => ({ assets: [{ ID: 9, Name: "cache", Type: "redis" }] }) },
}));
vi.mock("../openAsset", () => ({ openAssetConnection }));

import { deriveReferences, jumpToAsset, isAssetTabOpen } from "../aiReferences";

describe("deriveReferences", () => {
  it("collects unique mentioned assets from user messages, excluding the bound one", () => {
    const messages = [
      { role: "user", content: `${buildMentionXml({ assetId: 5, name: "web", type: "ssh" })} a` },
      { role: "assistant", content: "ignored" },
      { role: "user", content: `${buildMentionXml({ assetId: 9, name: "cache", type: "redis" })} ${buildMentionXml({ assetId: 5, name: "web", type: "ssh" })} b` },
    ];
    const refs = deriveReferences(messages, { excludeAssetId: 5 });
    expect(refs.map((r) => r.assetId)).toEqual([9]);
  });
});

describe("jumpToAsset", () => {
  beforeEach(() => vi.clearAllMocks());
  it("activates an already-open tab (reuse)", async () => {
    expect(isAssetTabOpen(5)).toBe(true);
    await jumpToAsset(5);
    expect(activateTab).toHaveBeenCalledWith("t1");
    expect(openAssetConnection).not.toHaveBeenCalled();
  });
  it("opens a new connection when no tab is open", async () => {
    expect(isAssetTabOpen(9)).toBe(false);
    await jumpToAsset(9);
    expect(openAssetConnection).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/aiReferences.test.ts ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 模块不存在。

- [ ] **Step 3: 实现**

Create `frontend/src/lib/aiReferences.ts`:

```ts
import type { MentionAttrs } from "./mentionXml";
import { extractMentions } from "./mentionXml";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { openAssetConnection } from "./openAsset";

/** 从会话消息里汇总被 @mention 的资产（去重保序，可排除已绑定主资产）。 */
export function deriveReferences(
  messages: { role: string; content: string }[],
  opts?: { excludeAssetId?: number | null }
): MentionAttrs[] {
  const seen = new Set<number>();
  const out: MentionAttrs[] = [];
  for (const m of messages) {
    if (m.role !== "user") continue;
    for (const mention of extractMentions(m.content)) {
      if (mention.assetId === opts?.excludeAssetId) continue;
      if (seen.has(mention.assetId)) continue;
      seen.add(mention.assetId);
      out.push(mention);
    }
  }
  return out;
}

/** 该资产是否已有打开的工作区 tab（决定 ↗ 复用 / + 新开）。 */
export function isAssetTabOpen(assetId: number): boolean {
  return useTabStore.getState().tabs.some((t) => {
    const meta = t.meta as { assetId?: number };
    return meta != null && meta.assetId === assetId;
  });
}

/** 跳转到资产：已开 tab 则聚焦；否则打开新连接。 */
export async function jumpToAsset(assetId: number): Promise<void> {
  const open = useTabStore.getState().tabs.find((t) => (t.meta as { assetId?: number })?.assetId === assetId);
  if (open) {
    useTabStore.getState().activateTab(open.id);
    return;
  }
  const asset = useAssetStore.getState().assets.find((a) => a.ID === assetId);
  if (asset) await openAssetConnection(asset);
}
```

- [ ] **Step 4: 跑测试确认通过 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/aiReferences.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: PASS；tsc 0 error。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/lib/aiReferences.ts frontend/src/lib/__tests__/aiReferences.test.ts
git commit -m "✨ 会话引用派生 + 资产跳转（复用已开 tab / 新开）"
```

---

### Task 8: `ReferencesRow` 组件挂入联动资产区

**Files:**
- Create: `frontend/src/components/ai/ReferencesRow.tsx`
- Modify: `frontend/src/components/ai/SideAssistantContextBar.tsx`（展开区加 `ReferencesRow`）
- Modify: `frontend/src/i18n/locales/{en,zh-CN}/common.json`
- Test: `frontend/src/components/ai/__tests__/ReferencesRow.test.tsx`

**Interfaces:**
- Consumes: `useAIStore`（`conversationMessages`/`getMessagesByConversationId`、当前 sidebarTab 的 `linkedAssetId`）、`deriveReferences`/`jumpToAsset`/`isAssetTabOpen`（T7）、`ai.sidebar.referencedThisSession`
- Props：`{ conversationId: number | null; boundAssetId?: number | null }`
- 行为：无引用时不渲染（返回 null）；有引用时渲染 `本次引用: [name ↗|+] …`，点击 chip → `jumpToAsset`。

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/ai/__tests__/ReferencesRow.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { buildMentionXml } from "@/lib/mentionXml";
import { ReferencesRow } from "../ReferencesRow";
import { useAIStore } from "@/stores/aiStore";

const jumpToAsset = vi.fn().mockResolvedValue(undefined);
vi.mock("@/lib/aiReferences", async (orig) => {
  const actual = await orig<typeof import("@/lib/aiReferences")>();
  return { ...actual, jumpToAsset, isAssetTabOpen: (id: number) => id === 5 };
});

describe("ReferencesRow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAIStore.setState({
      conversationMessages: {
        1: [
          { role: "user", content: `${buildMentionXml({ assetId: 5, name: "web", type: "ssh" })} ${buildMentionXml({ assetId: 9, name: "cache", type: "redis" })}`, blocks: [] } as any,
        ],
      },
    });
  });

  it("renders referenced assets excluding the bound one and jumps on click", () => {
    render(<ReferencesRow conversationId={1} boundAssetId={5} />);
    // 5 被排除（是绑定资产），只剩 cache(9)
    expect(screen.queryByText("web")).toBeNull();
    fireEvent.click(screen.getByText("cache"));
    expect(jumpToAsset).toHaveBeenCalledWith(9);
  });

  it("renders nothing when there are no references", () => {
    const { container } = render(<ReferencesRow conversationId={999} boundAssetId={null} />);
    expect(container).toBeEmptyDOMElement();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/ReferencesRow.test.tsx ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 组件不存在。

- [ ] **Step 3: 加 i18n**

`en/common.json` `ai.sidebar` 加：`"referencedThisSession": "Referenced",`。
`zh-CN/common.json` `ai.sidebar` 加：`"referencedThisSession": "本次引用",`。

- [ ] **Step 4: 实现组件**

Create `frontend/src/components/ai/ReferencesRow.tsx`:

```tsx
import { useTranslation } from "react-i18next";
import { ArrowUpRight, Plus } from "lucide-react";
import { useAIStore } from "@/stores/aiStore";
import { deriveReferences, jumpToAsset, isAssetTabOpen } from "@/lib/aiReferences";

export function ReferencesRow({ conversationId, boundAssetId }: { conversationId: number | null; boundAssetId?: number | null }) {
  const { t } = useTranslation();
  const messages = useAIStore((s) => (conversationId != null ? s.conversationMessages[conversationId] : undefined));
  if (conversationId == null) return null;
  const refs = deriveReferences(messages ?? [], { excludeAssetId: boundAssetId });
  if (refs.length === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-1.5 pt-1 text-xs text-muted-foreground" data-testid="references-row">
      <span className="shrink-0">{t("ai.sidebar.referencedThisSession")}:</span>
      {refs.map((r) => {
        const open = isAssetTabOpen(r.assetId);
        return (
          <button
            key={r.assetId}
            onClick={() => jumpToAsset(r.assetId)}
            title={open ? t("ai.sidebar.referencedThisSession") : t("ai.sidebar.referencedThisSession")}
            className="inline-flex items-center gap-1 rounded border border-border bg-secondary px-1.5 py-0.5 hover:bg-secondary/70"
          >
            <span className="truncate max-w-[120px]">{r.name}</span>
            {open ? <ArrowUpRight className="h-3 w-3" /> : <Plus className="h-3 w-3" />}
          </button>
        );
      })}
    </div>
  );
}
```

在 `SideAssistantContextBar.tsx` 展开区（`#ai-linked-asset-section` div 内，`LinkedAssetControl` 之后）挂：

```tsx
      <div id="ai-linked-asset-section" data-testid="linked-asset-section" className="px-3 pb-2">
        <LinkedAssetControl sidebarTabId={sidebarTabId} />
        <ReferencesRow
          conversationId={conversationId}
          boundAssetId={useAIStore.getState().sidebarTabs.find((tb) => tb.id === sidebarTabId)?.linkedAssetId ?? null}
        />
      </div>
```
> 顶部 import `ReferencesRow` 与（若未有）`useAIStore`。`boundAssetId` 用一次性读取即可（引用行不需随绑定实时重算到毫秒级）；若想更响应式，可在组件内用 `useAIStore` selector 取 bound id——实现者按简洁优先。

- [ ] **Step 5: 跑测试确认通过 + i18n 对齐 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/ReferencesRow.test.tsx src/__tests__/i18n.test.ts && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: 全绿。

- [ ] **Step 6: 提交**

```bash
git add frontend/src/components/ai/ReferencesRow.tsx frontend/src/components/ai/SideAssistantContextBar.tsx frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "✨ 联动资产区新增本次引用行（点击复用/新开 tab）"
```

---

### Task 9: 会话轨头像取绑定资产图标

**Files:**
- Modify: `frontend/src/components/ai/SideAssistantTabBar.tsx`
- Test: `frontend/src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx`（新建）

**Interfaces:**
- Consumes: `SidebarAITab.linkedAssetType`、`getAssetType(type)?.icon`（`@/lib/assetTypes`）、现有 `getSessionIconColor`/`getSessionIconLetter`
- 行为：会话项有 `linkedAssetType` 且 `getAssetType` 命中 → 头像渲染该类型图标（保留取色底 + 状态点 + 激活 ring）；否则回退现有首字。

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { SideAssistantTabBar } from "../SideAssistantTabBar";

const baseTab = { id: "s1", conversationId: 1, title: "prod-web-01", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } };

function renderBar(tabExtra: object) {
  return render(
    <SideAssistantTabBar
      tabs={[{ ...baseTab, ...tabExtra } as any]}
      activeTabId="s1"
      getStatus={() => "done" as any}
      collapsed={false}
      onActivate={vi.fn()}
      onClose={vi.fn()}
      onNewChat={vi.fn()}
      onToggleCollapsed={vi.fn()}
    />
  );
}

describe("SideAssistantTabBar avatar", () => {
  it("renders the asset-type icon when the tab is bound", () => {
    renderBar({ linkedAssetId: 42, linkedAssetType: "ssh", linkedAssetName: "prod-web-01" });
    expect(screen.getByTestId("session-asset-icon-s1")).toBeInTheDocument();
  });

  it("falls back to the title letter when unbound", () => {
    renderBar({});
    expect(screen.queryByTestId("session-asset-icon-s1")).toBeNull();
    expect(screen.getByText("P")).toBeInTheDocument(); // getSessionIconLetter("prod-web-01") → "P"
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: FAIL — 无 `session-asset-icon-s1`。

- [ ] **Step 3: 实现**

`SideAssistantTabBar.tsx` 顶部 import：`import { getAssetType } from "@/lib/assetTypes";`。在渲染头像内容处（展开态 `:175-197` 与折叠态 `:104-128` 两处）抽一个内联渲染：给每个 tab 计算 `const AssetIcon = tab.linkedAssetType ? getAssetType(tab.linkedAssetType)?.icon : undefined;`，头像内容改为：

```tsx
{AssetIcon ? (
  <AssetIcon data-testid={`session-asset-icon-${tab.id}`} className="h-3.5 w-3.5" />
) : (
  letter
)}
```

（保留 `color.bg`/`color.fg` 取色底、状态点、激活 `ring-2 ring-primary`、running 的 `LoaderCircle` 语义——只替换「首字 `letter`」这一层内容。两处渲染统一处理，避免只改一处。）
> 若 `getAssetType(type)?.icon` 组件不接受 `data-testid`（部分 lucide 包装组件会透传，部分不会），改为在外层 `<span data-testid=...>` 包裹图标；实现者按组件实际 props 决定，保证测试可选中。

- [ ] **Step 4: 跑测试确认通过 + tsc**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx && pnpm exec tsc --noEmit ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: PASS（2）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/ai/SideAssistantTabBar.tsx frontend/src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx
git commit -m "✨ 会话轨头像取绑定资产类型图标（回退标题首字）"
```

---

### Task 10: 全量回归（后端 + 前端）

**Files:** 无源码改动（仅验证 / 视需要补漏）

- [ ] **Step 1: 后端全量**

```bash
go test ./internal/ai/... 2>&1 | tail -20
golangci-lint run ./internal/ai/... 2>&1 | tail -20
```
Expected: 全 PASS；lint 0 issue。

- [ ] **Step 2: 前端全量 + 类型检查**

```bash
cd frontend && ( pnpm exec tsc --noEmit && pnpm test ) ; cd .. && git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
```
Expected: tsc 0 error；全部前端测试绿（含 i18n 对齐、Plan 1 既有测试、本计划新增测试）。

- [ ] **Step 3: 若有失败**：定位并修复（在对应 Task 的文件内，遵循 Fix policy——先复现再修，不扩范围），补测后重跑至全绿，单独提交 `🐛 …`。若全绿则无需提交。

---

## Self-Review

**Spec coverage（对 `2026-07-09-ai-follow-terminal-design.md`）：**
- §4.4 上下文正确性（`TabInfo` 加 active / prompt 强调主资产）→ T1（Go）+ T2（前端置顶设 active）。✓（真实字段，非 Plan 1 的排序近似）
- §4.2 / §4.6 ②③ `🔗 跟随`（默认关、收进 `▾` 菜单、切换终端重绑、chip 前缀 `🔗`）→ T3（字段 + 开启即绑）+ T4（切换重绑订阅 + 轻提示）+ T5（`▾` 菜单 + `🔗` 前缀）。✓
- §4.2 / §4.6 ⑤ 本次引用行 + 点击跳转（↗ 复用 / + 新开）→ T6（抽 `openAssetConnection`）+ T7（`deriveReferences`/`jumpToAsset`）+ T8（`ReferencesRow`）。✓（仅 @mention 来源；工具调用命中来源见 spec 开放问题 2，明确不做）
- §4.3 / §4.6 ⑥ 会话轨头像取绑定资产 → T9。✓
- §4.5 边界态（未绑定 / tab 关闭断连 / 提升为 tab）：未绑定 chip 由 Plan 1 覆盖；断连置灰属渲染细节，本计划不新增（chip 仍保留绑定，可 `▾` 重绑——菜单已给）；提升为 tab 复用同 `SideAssistantContextBar`，行为一致。✓（无新代码需求）

**Placeholder scan：** 每步含真实代码/命令/期望。两处显式标注「实现者按实际调整」：T5 Radix 菜单在测试环境的展开方式、T9 图标组件是否透传 `data-testid`——均给了确定的回退方案（抽 handler 单测 / 外层 span 包裹），非悬空 TODO。✓

**Type consistency：**
- `tabToAssetRef` 返回 `{assetId,assetName,assetType}`（T3）——与 `setSidebarTabAsset` 入参同形（Plan 1），T3/T4 直接喂入。✓
- `TabInfo.active`（Go `bool` → TS `boolean`）：T1 定义、T2 消费、生成物经 `wails generate module` 对齐。⚠ **依赖 T2 Step 1 重生成成功**（或手补 gitignore 文件）——已给失败回退。
- `MentionAttrs`（`{assetId,name,type?,...}`）：T7 `deriveReferences` 产出、T8 消费 `r.assetId`/`r.name`。✓
- `openAssetConnection(asset: asset_entity.Asset)`：T6 定义、T7 `jumpToAsset` 消费。✓
- `SidebarAITab.followActiveTerminal?: boolean`：T3 定义、T4 订阅读、T5 菜单读写。✓

**已知风险 / 依赖：**
- **wailsjs 重生成**（T2 Step 1）是 T2 及其后所有前端类型检查的前置。若本机 `wails generate module` 不可用，手补 gitignore 的 `models.ts`（已在 Global Constraints + T2 说明）。
- **Radix `DropdownMenu` 测试可展开性**（T5）：happy-dom/jsdom 下 Radix 菜单内容挂载时机可能致测试点不到菜单项——已给「抽 handler 单测」回退，保证 TDD 可行。
- **跨 store 订阅**（T4）：`useTabStore.subscribe` 在 aiStore 模块级注册，是本仓新模式（现有 `.subscribe` 均为自订阅持久化）——已按现有 idiom 写，注意去重 `__lastActiveTabId` 防重复触发。
- **T5 与 Plan 1 的 T6 测试**：原 `LinkedAssetControl.test.tsx` 可能断言旧的独立 `linked-asset-clear` 按钮；T5 把清除迁进菜单，需同步更新那条断言（Step 5 命令已并跑该文件以暴露回归）。
