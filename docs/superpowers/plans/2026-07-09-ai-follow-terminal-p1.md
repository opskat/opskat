# AI 跟随终端 — Plan 1（核心绑定 + 上下文正确性）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 AI 侧边助手会话可**显式关联一个主资产**（手动 或 @mention 自动），并保证该资产进入 AI 上下文；顶部「关联资产」区默认收起、默认未绑定。

**Architecture:** 纯前端增量（无后端改动）。在 `SidebarAITab` 上新增 `linkedAssetId`（+ 冗余 name/type）；新增 `setSidebarTabAsset` / `clearSidebarTabAsset` store action；`_sendForConversation` 组装 `AIContext.openTabs` 时把绑定资产**置顶且保证存在**；`SideAssistantContextBar` 升级为可折叠（标题行常驻 + `关联资产 ⌄`），展开区渲染绑定 chip / `绑定资产` 入口（复用 `AssetSelect`）；`sendFromSidebarTab` 在未绑定会话里遇首个 @mention 时自动绑定。

**Tech Stack:** React 19 + TypeScript（strict）+ Zustand（`useAIStore`/`useTabStore`）+ react-i18next + Vitest + @testing-library/react + `@opskat/ui`（`TreeSelect`/`AssetSelect`）。构建为 Wails v2。

## Global Constraints

- 前端命令用 **pnpm**（`pnpm test` / `pnpm lint` / `pnpm exec tsc --noEmit`）。测试框架 **Vitest**。
- **ENV 陷阱**：本机 `pnpm test`/`pnpm lint` 会重写 `frontend/pnpm-lock.yaml`（丢 dompurify override）。每次跑完 **`git checkout -- frontend/pnpm-lock.yaml`**，且提交时**只显式 `git add` 目标文件**，绝不 `git add -A`。
- **wailsjs 是 gitignore 生成物**：本 worktree 首次实现前需 `cd frontend && pnpm install`（deps）并生成 `frontend/wailsjs`（`wails generate module`，或从主检出 `/Users/codfrm/Code/opskat/opskat/frontend/wailsjs` 拷入）——否则 import 无法解析、测试跑不起来。
- **i18n 双语必填**：所有新文案同时加到 `frontend/src/i18n/locales/en/common.json` 与 `zh-CN/common.json`；各语言用**地道表达**，不逐字对齐。
- **Toast**：成功走 `notify.ts`（`notifySuccess` 顶部居中）；错误用 `toast.error`（右下角）。不直接 `toast.success`。
- **OCP**：不在共享代码里 `if (assetType === "ssh")` 分支；资产类型从 `Tab.meta.assetType` / `MentionAttrs.type` 直接读。
- 提交用 **gitmoji** 前缀；TDD 微提交 subject **不带** `#160`（非刻意关联/关闭 issue）。
- 每步跑测试前确保 `frontend/wailsjs` 存在；每步提交前 `git checkout -- frontend/pnpm-lock.yaml`。

## File Structure

| 文件 | 责任 | 动作 |
|---|---|---|
| `frontend/src/stores/aiStore.ts` | `SidebarAITab` 字段、`createSidebarTab`/`sanitizeSidebarTab`、`didSidebarStructureChange`、`setSidebarTabAsset`/`clearSidebarTabAsset`、`_sendForConversation` 上下文、`sendFromSidebarTab` 自动绑定 | Modify |
| `frontend/src/lib/aiLinkedAsset.ts` | 纯函数：`LinkedAsset` 类型 + `linkedAssetFromMention()`（MentionAttrs→绑定元数据） | Create |
| `frontend/src/components/ai/LinkedAssetControl.tsx` | 绑定 chip / `绑定资产` 入口 + `AssetSelect` 换绑 + 清除（读写 store） | Create |
| `frontend/src/components/ai/SideAssistantContextBar.tsx` | 升级为可折叠：标题行 + `关联资产 ⌄` 展开开关（持久化收起态）+ 展开区挂 `LinkedAssetControl` | Modify |
| `frontend/src/i18n/locales/{en,zh-CN}/common.json` | `ai.sidebar.linkedAsset.*` + `ai.sidebar.contextSection` 文案 | Modify |
| `frontend/src/lib/__tests__/aiLinkedAsset.test.ts` | T7 helper 单测 | Create |
| `frontend/src/__tests__/aiStore.linkedAsset.test.ts` | T1/T2/T3/T7 store 单测 | Create |
| `frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx` | T6 组件测试 | Create |
| `frontend/src/components/ai/__tests__/SideAssistantContextBar.collapse.test.tsx` | T5 折叠测试 | Create |

**Deferred to Plan 2**（不在本计划）：`🔗 跟随`开关 + 切换终端重绑、`本次引用` 派生与点击跳转、会话轨头像取绑定资产、后端 `AIContext.active/primaryAssetId` 字段与 prompt 强调。

---

### Task 1: `linkedAssetId` 字段 + sanitize round-trip

**Files:**
- Modify: `frontend/src/stores/aiStore.ts` (SidebarAITab `:157-163`; `createSidebarTab` `:225-233`; `sanitizeSidebarTab` `:258-271`)
- Test: `frontend/src/__tests__/aiStore.linkedAsset.test.ts`

**Interfaces:**
- Produces: `interface SidebarAITab { …; linkedAssetId?: number | null; linkedAssetName?: string; linkedAssetType?: string }`
- Produces: `export function sanitizeSidebarTab(raw: unknown): SidebarAITab | null`（改为导出，供测试）

- [ ] **Step 1: 写失败测试**

Create `frontend/src/__tests__/aiStore.linkedAsset.test.ts`:

```ts
import { describe, it, expect } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { sanitizeSidebarTab } from "../stores/aiStore";

describe("sanitizeSidebarTab linked asset", () => {
  it("round-trips valid linked asset fields", () => {
    const tab = sanitizeSidebarTab({
      id: "sidebar-1",
      conversationId: 7,
      title: "t",
      createdAt: 1,
      linkedAssetId: 42,
      linkedAssetName: "prod-web-01",
      linkedAssetType: "ssh",
    });
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("drops non-number linkedAssetId to undefined", () => {
    const tab = sanitizeSidebarTab({ id: "s2", linkedAssetId: "nope" });
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
```

Add `import { vi } from "vitest";` at top (or include in the destructure).

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — `sanitizeSidebarTab` 非导出 / 未处理 linked 字段。

- [ ] **Step 3: 实现**

In `aiStore.ts`, extend the interface (`:157`):

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
}
```

In `createSidebarTab` (`:225`) pass the fields through:

```ts
function createSidebarTab(overrides?: Partial<SidebarAITab>): SidebarAITab {
  return {
    id: overrides?.id ?? createSidebarTabId(),
    conversationId: overrides?.conversationId ?? null,
    title: overrides?.title ?? getDefaultSidebarTitle(),
    createdAt: overrides?.createdAt ?? Date.now(),
    uiState: createDefaultSidebarUiState(overrides?.uiState),
    linkedAssetId: typeof overrides?.linkedAssetId === "number" ? overrides.linkedAssetId : undefined,
    linkedAssetName: overrides?.linkedAssetName,
    linkedAssetType: overrides?.linkedAssetType,
  };
}
```

Change `sanitizeSidebarTab` (`:258`) to export + round-trip:

```ts
export function sanitizeSidebarTab(raw: unknown): SidebarAITab | null {
  if (!raw || typeof raw !== "object") return null;
  const tab = raw as Partial<SidebarAITab>;
  if (typeof tab.id !== "string" || tab.id.length === 0) return null;
  const conversationId =
    typeof tab.conversationId === "number" && Number.isFinite(tab.conversationId) ? tab.conversationId : null;
  return createSidebarTab({
    id: tab.id,
    conversationId,
    title: typeof tab.title === "string" && tab.title.length > 0 ? tab.title : undefined,
    createdAt: typeof tab.createdAt === "number" && Number.isFinite(tab.createdAt) ? tab.createdAt : undefined,
    uiState: sanitizeSidebarUiStateForPersistence(tab.uiState),
    linkedAssetId: typeof tab.linkedAssetId === "number" && Number.isFinite(tab.linkedAssetId) ? tab.linkedAssetId : undefined,
    linkedAssetName: typeof tab.linkedAssetName === "string" ? tab.linkedAssetName : undefined,
    linkedAssetType: typeof tab.linkedAssetType === "string" ? tab.linkedAssetType : undefined,
  });
}
```

`persistSidebarTabs` (`:279`) 用 `...tab` 展开，自动带上新字段——无需改。

- [ ] **Step 4: 跑测试确认通过**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（2 tests）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.linkedAsset.test.ts
git commit -m "✨ SidebarAITab 增加 linkedAsset 绑定字段 + sanitize round-trip"
```

---

### Task 2: `setSidebarTabAsset` / `clearSidebarTabAsset` action + 即时持久化

**Files:**
- Modify: `frontend/src/stores/aiStore.ts` (AIState `:1604`; actions 类型 `:1620-1676`; return object；`didSidebarStructureChange` `:2337-2343`)
- Test: `frontend/src/__tests__/aiStore.linkedAsset.test.ts`（追加）

**Interfaces:**
- Consumes: `SidebarAITab.linkedAssetId`（T1）
- Produces: `setSidebarTabAsset(tabId: string, asset: { assetId: number; assetName: string; assetType: string }): void`
- Produces: `clearSidebarTabAsset(tabId: string): void`

- [ ] **Step 1: 写失败测试**（追加到 aiStore.linkedAsset.test.ts）

```ts
import { useAIStore } from "../stores/aiStore";

describe("setSidebarTabAsset / clearSidebarTabAsset", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
      activeSidebarTabId: "s1",
    });
  });

  it("binds an asset to the tab and persists it", () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 42, assetName: "prod-web-01", assetType: "ssh" });
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    const persisted = JSON.parse(localStorage.getItem("ai_sidebar_tabs") || "[]");
    expect(persisted[0].linkedAssetId).toBe(42);
  });

  it("clears the binding", () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 42, assetName: "p", assetType: "ssh" });
    useAIStore.getState().clearSidebarTabAsset("s1");
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — `setSidebarTabAsset is not a function`。

- [ ] **Step 3: 实现**

In `AIState`（`:1604` 区块，动作声明约 `:1649` 附近）加类型：

```ts
  setSidebarTabAsset: (tabId: string, asset: { assetId: number; assetName: string; assetType: string }) => void;
  clearSidebarTabAsset: (tabId: string) => void;
```

In the store return object（紧邻 `activateSidebarTab` 实现处，约 `:1898`）加实现：

```ts
    setSidebarTabAsset: (tabId, asset) => {
      set((state) => ({
        sidebarTabs: state.sidebarTabs.map((tab) =>
          tab.id === tabId
            ? { ...tab, linkedAssetId: asset.assetId, linkedAssetName: asset.assetName, linkedAssetType: asset.assetType }
            : tab
        ),
      }));
    },
    clearSidebarTabAsset: (tabId) => {
      set((state) => ({
        sidebarTabs: state.sidebarTabs.map((tab) =>
          tab.id === tabId
            ? { ...tab, linkedAssetId: undefined, linkedAssetName: undefined, linkedAssetType: undefined }
            : tab
        ),
      }));
    },
```

让绑定变更立刻落盘：把 `didSidebarStructureChange`（`:2337`）的比较加上 `linkedAssetId`：

```ts
    if (a.id !== b.id || a.conversationId !== b.conversationId || a.title !== b.title || a.linkedAssetId !== b.linkedAssetId) return true;
```

（persist 由 `:2346` 的 subscribe 自动触发；`sidebarTabs` 引用已变，加上 structure-change 命中即走 `flushSidebarPersist`。）

- [ ] **Step 4: 跑测试确认通过**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（4 tests）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.linkedAsset.test.ts
git commit -m "✨ aiStore 增加 setSidebarTabAsset/clearSidebarTabAsset + 即时持久化"
```

---

### Task 3: 绑定资产保证进入 `AIContext.openTabs`（置顶）

**Files:**
- Modify: `frontend/src/stores/aiStore.ts` (`_sendForConversation` openTabs 组装 `:1573-1589`)
- Test: `frontend/src/__tests__/aiStore.linkedAsset.test.ts`（追加）

**Interfaces:**
- Consumes: `SidebarAITab.linkedAssetId/Name/Type`（T1）、`runner.TabInfo`、`SendAIMessage`
- 行为：`_sendForConversation(convId)` 查 `sidebarTabs.find(t => t.conversationId === convId)`；若其 `linkedAssetId` 有值且不在 openTabs 中 → 以 `TabInfo{type,assetId,assetName}` **prepend**（置顶、去重）。

- [ ] **Step 1: 写失败测试**（追加）

```ts
import { SendAIMessage } from "../../wailsjs/go/ai/AI";

describe("_sendForConversation includes linked asset in context", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true,
      modelName: "gpt-4",
      conversationMessages: { 1: [] },
      conversationStreaming: { 1: { sending: false, pendingQueue: [] } },
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null }, linkedAssetId: 99, linkedAssetName: "cache", linkedAssetType: "redis" }],
      activeSidebarTabId: "s1",
    });
  });

  it("prepends the bound asset even when its tab is not open", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number; assetName: string; type: string }> };
    expect(aiContext.openTabs[0].assetId).toBe(99);
    expect(aiContext.openTabs[0].assetName).toBe("cache");
    expect(aiContext.openTabs[0].type).toBe("redis");
  });

  it("does not duplicate when the bound asset tab is already open", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
    useTabStore.setState({
      tabs: [{ id: "q1", type: "query", label: "cache", meta: { assetId: 99, assetName: "cache", assetType: "redis" } } as any],
      activeTabId: "q1",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.filter((t) => t.assetId === 99)).toHaveLength(1);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — `openTabs[0].assetId` 不是 99（绑定资产未置顶/未包含）。

- [ ] **Step 3: 实现**

替换 `_sendForConversation` 里 openTabs 组装块（`:1573-1589`），在构造 `aiContext` 前插入绑定资产：

```ts
  // 收集当前 Tab 上下文
  const allTabs = useTabStore.getState().tabs;
  const openTabs = allTabs
    .filter(
      (t): t is Tab & { meta: { assetId: number; assetName?: string } } =>
        t.type !== "ai" && t.type !== "page" && t.meta != null && "assetId" in t.meta
    )
    .map((t) => {
      const type = t.type === "query" ? (t.meta as QueryTabMeta).assetType : t.type === "terminal" ? "ssh" : t.type;
      return new runner.TabInfo({
        type,
        assetId: t.meta.assetId || 0,
        assetName: t.meta.assetName || t.label || "",
      });
    });

  // 会话若绑定了主资产，保证它在上下文里且置顶（即使对应 tab 未打开）。
  const boundTab = useAIStore.getState().sidebarTabs.find((tab) => tab.conversationId === convId);
  if (boundTab?.linkedAssetId != null) {
    const already = openTabs.some((t) => t.assetId === boundTab.linkedAssetId);
    const rest = already ? openTabs.filter((t) => t.assetId !== boundTab.linkedAssetId) : openTabs;
    openTabs.length = 0;
    openTabs.push(
      new runner.TabInfo({
        type: boundTab.linkedAssetType || "",
        assetId: boundTab.linkedAssetId,
        assetName: boundTab.linkedAssetName || "",
      }),
      ...rest
    );
  }

  const aiContext = new runner.AIContext({ openTabs });
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（6 tests）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.linkedAsset.test.ts
git commit -m "✨ AI 上下文置顶绑定的主资产并去重"
```

---

### Task 4: i18n 文案（en / zh-CN）

**Files:**
- Modify: `frontend/src/i18n/locales/en/common.json`、`frontend/src/i18n/locales/zh-CN/common.json`（`ai.sidebar` 节点下）
- Test: `frontend/src/__tests__/i18n.test.ts`（已存在，校验 en/zh key 对齐）

**Interfaces:**
- Produces keys（`ai.sidebar.` 前缀）：`contextSection`、`contextExpand`、`contextCollapse`、`linkedAsset.bind`、`linkedAsset.change`、`linkedAsset.clear`、`linkedAsset.unbound`、`linkedAsset.pickPlaceholder`

- [ ] **Step 1: 加 en 文案**

In `en/common.json`，`ai.sidebar` 对象内追加：

```json
"contextSection": "Linked assets",
"contextExpand": "Show linked assets",
"contextCollapse": "Hide linked assets",
"linkedAsset": {
  "bind": "Link an asset",
  "change": "Change",
  "clear": "Clear",
  "unbound": "Not linked",
  "pickPlaceholder": "Choose an asset"
}
```

- [ ] **Step 2: 加 zh-CN 文案**

In `zh-CN/common.json`，`ai.sidebar` 对象内追加：

```json
"contextSection": "关联资产",
"contextExpand": "展开关联资产",
"contextCollapse": "收起关联资产",
"linkedAsset": {
  "bind": "绑定资产",
  "change": "换绑",
  "clear": "清除",
  "unbound": "未绑定",
  "pickPlaceholder": "选择要绑定的资产"
}
```

- [ ] **Step 3: 跑 i18n 对齐测试**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/i18n.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（en/zh key 集合一致）。若测试报某侧缺 key，补齐该 key。

- [ ] **Step 4: 提交**

```bash
git add frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "🌐 新增 ai.sidebar 关联资产文案 (en/zh)"
```

---

### Task 5: `SideAssistantContextBar` 升级为可折叠（默认收起）

**Files:**
- Modify: `frontend/src/components/ai/SideAssistantContextBar.tsx`
- Modify: `frontend/src/components/ai/SideAssistantPanel.tsx`（给 ContextBar 传 `sidebarTabId`，`:189`）
- Test: `frontend/src/components/ai/__tests__/SideAssistantContextBar.collapse.test.tsx`

**Interfaces:**
- Consumes: `LinkedAssetControl`（T6，先用占位；T6 完成后接线）、`ai.sidebar.contextSection`（T4）
- Produces: `SideAssistantContextBar` 新增 prop `sidebarTabId: string | null`
- 折叠态持久化 key：`localStorage["ai_context_collapsed"]`，默认 `true`（收起）

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/ai/__tests__/SideAssistantContextBar.collapse.test.tsx`:

```tsx
import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SideAssistantContextBar } from "../SideAssistantContextBar";
import { useAIStore } from "@/stores/aiStore";

describe("SideAssistantContextBar collapse", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "会话" } as any],
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "会话", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
      activeSidebarTabId: "s1",
    });
  });

  it("hides the linked-assets section by default (collapsed)", () => {
    render(<SideAssistantContextBar conversationId={1} sidebarTabId="s1" />);
    expect(screen.queryByTestId("linked-asset-section")).toBeNull();
  });

  it("reveals the section after clicking the disclosure", () => {
    render(<SideAssistantContextBar conversationId={1} sidebarTabId="s1" />);
    fireEvent.click(screen.getByTestId("context-disclosure"));
    expect(screen.getByTestId("linked-asset-section")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/SideAssistantContextBar.collapse.test.tsx ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — 无 `context-disclosure` / prop 不接受。

- [ ] **Step 3: 实现**

改 `SideAssistantContextBar.tsx`：props 加 `sidebarTabId`，标题行加展开开关，展开时渲染带 `data-testid="linked-asset-section"` 的容器（内含 `LinkedAssetControl` 占位；T6 接线）。在非编辑态 return（`:135`）替换为：

```tsx
import { ChevronDown, ChevronUp } from "lucide-react";
import { LinkedAssetControl } from "./LinkedAssetControl";

// props：
interface SideAssistantContextBarProps {
  conversationId: number | null;
  sidebarTabId: string | null;
}

// 组件内、renameState 之后：
const [contextExpanded, setContextExpanded] = useState(() => localStorage.getItem("ai_context_collapsed") === "false");
const toggleContext = () => {
  setContextExpanded((prev) => {
    const next = !prev;
    localStorage.setItem("ai_context_collapsed", String(!next));
    return next;
  });
};

// 非编辑态 return：
return (
  <div className="border-b border-panel-divider">
    <div className="flex items-center gap-2 px-3 py-1.5 text-xs text-muted-foreground">
      <span className="truncate flex-1 text-foreground" onDoubleClick={startRename}>
        {conversationTitle || t("ai.newConversation")}
      </span>
      <Button variant="ghost" size="icon" className="h-6 w-6 shrink-0" onClick={startRename}
        title={t("ai.renameConversation")} aria-label={t("ai.renameConversation")} disabled={!conv}>
        <Pencil className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="icon" className="h-6 w-6 shrink-0" onClick={toggleContext}
        data-testid="context-disclosure"
        title={contextExpanded ? t("ai.sidebar.contextCollapse") : t("ai.sidebar.contextExpand")}
        aria-label={contextExpanded ? t("ai.sidebar.contextCollapse") : t("ai.sidebar.contextExpand")}
        aria-expanded={contextExpanded}>
        {contextExpanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
      </Button>
    </div>
    {contextExpanded && (
      <div data-testid="linked-asset-section" className="px-3 pb-2">
        <LinkedAssetControl sidebarTabId={sidebarTabId} />
      </div>
    )}
  </div>
);
```

在 `SideAssistantPanel.tsx`（`:189`）把 `sidebarTabId` 传进去：

```tsx
<SideAssistantContextBar key={activeConversationId ?? "empty"} conversationId={activeConversationId} sidebarTabId={activeSidebarTab?.id ?? null} />
```

（本任务先建最小 `LinkedAssetControl` 占位以过测试；完整实现在 T6。占位：）

Create `frontend/src/components/ai/LinkedAssetControl.tsx`:

```tsx
export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  return <div data-testid="linked-asset-control">{sidebarTabId}</div>;
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/SideAssistantContextBar.collapse.test.tsx ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（2 tests）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/ai/SideAssistantContextBar.tsx frontend/src/components/ai/SideAssistantPanel.tsx frontend/src/components/ai/LinkedAssetControl.tsx frontend/src/components/ai/__tests__/SideAssistantContextBar.collapse.test.tsx
git commit -m "✨ ContextBar 升级为可折叠关联资产区（默认收起）"
```

---

### Task 6: `LinkedAssetControl`（绑定 chip + AssetSelect 换绑 + 清除）

**Files:**
- Modify: `frontend/src/components/ai/LinkedAssetControl.tsx`（替换 T5 占位）
- Test: `frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx`

**Interfaces:**
- Consumes: `useAIStore`（`sidebarTabs`/`setSidebarTabAsset`/`clearSidebarTabAsset`）、`useAssetStore`（`getAsset`，取 name/type/icon）、`AssetSelect`（`value:number`/`onValueChange:(id)=>void`）、`ai.sidebar.linkedAsset.*`
- 行为：未绑定 → 展示 `绑定资产` 触发 `AssetSelect`；选中 → `setSidebarTabAsset`（name/type 取自 `useAssetStore.getAsset(id)`）。已绑定 → chip 显示 name + `换绑`/`清除`。

- [ ] **Step 1: 写失败测试**

Create `frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: ({ onValueChange }: { onValueChange: (id: number) => void }) => (
    <button data-testid="asset-pick" onClick={() => onValueChange(42)}>pick</button>
  ),
}));

describe("LinkedAssetControl", () => {
  beforeEach(() => {
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
      activeSidebarTabId: "s1",
    });
  });

  it("binds the picked asset via setSidebarTabAsset", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    fireEvent.click(screen.getByTestId("asset-pick"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("shows the bound chip and clears on clear", () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 42, assetName: "prod-web-01", assetType: "ssh" });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    expect(screen.getByText("prod-web-01")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("linked-asset-clear"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
```

Verify `useAssetStore` 的资产字段名（`ID`/`Name`/`Type`/`Icon`）与实际一致（读 `frontend/src/stores/assetStore.ts` 的 asset 类型）；若不同，按实际调整测试与实现。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/LinkedAssetControl.test.tsx ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — 占位组件无 `asset-pick`/`linked-asset-clear`。

- [ ] **Step 3: 实现**

Replace `LinkedAssetControl.tsx`:

```tsx
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { X } from "lucide-react";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const setAsset = useAIStore((s) => s.setSidebarTabAsset);
  const clearAsset = useAIStore((s) => s.clearSidebarTabAsset);
  const getAsset = useAssetStore((s) => s.getAsset);
  const [picking, setPicking] = useState(false);

  if (!sidebarTabId) return null;

  const handlePick = (assetId: number) => {
    const asset = getAsset(assetId);
    if (!asset) return;
    setAsset(sidebarTabId, { assetId, assetName: asset.Name, assetType: asset.Type });
    setPicking(false);
  };

  if (tab?.linkedAssetId != null) {
    return (
      <div className="flex items-center gap-2" data-testid="linked-asset-chip">
        <span className="inline-flex items-center gap-1.5 rounded-md border border-border bg-secondary px-2 py-0.5 text-xs">
          <span className="h-1.5 w-1.5 rounded-full bg-success" />
          {tab.linkedAssetName}
        </span>
        <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => setPicking(true)}>
          {t("ai.sidebar.linkedAsset.change")}
        </Button>
        <Button variant="ghost" size="icon" className="h-6 w-6" data-testid="linked-asset-clear"
          onClick={() => clearAsset(sidebarTabId)}
          title={t("ai.sidebar.linkedAsset.clear")} aria-label={t("ai.sidebar.linkedAsset.clear")}>
          <X className="h-3.5 w-3.5" />
        </Button>
        {picking && (
          <AssetSelect value={tab.linkedAssetId} onValueChange={handlePick} placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")} testId="linked-asset-picker" />
        )}
      </div>
    );
  }

  return picking ? (
    <AssetSelect value={0} onValueChange={handlePick} placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")} testId="linked-asset-picker" />
  ) : (
    <Button variant="outline" size="sm" className="h-7 text-xs" data-testid="linked-asset-bind" onClick={() => setPicking(true)}>
      {t("ai.sidebar.linkedAsset.bind")}
    </Button>
  );
}
```

若 T6 测试用了 mock 的 AssetSelect（直接触发 `onValueChange(42)`），需绕过 `picking` gate：把未绑定态改为始终渲染 `AssetSelect`（不用 `picking`）以便测试点击。简化实现——未绑定态直接渲染：

```tsx
  return (
    <div data-testid="linked-asset-bind-row" className="flex items-center gap-2">
      <span className="text-xs text-muted-foreground">{t("ai.sidebar.linkedAsset.unbound")}</span>
      <AssetSelect value={0} onValueChange={handlePick} placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")} testId="linked-asset-picker" />
    </div>
  );
```

（采用后者：去掉 `picking` 状态与未绑定分支的按钮 gate，未绑定即显示 `未绑定` + 选择器；已绑定态的换绑仍用 `picking`。）

- [ ] **Step 4: 跑测试确认通过**

```bash
cd frontend && ( pnpm exec vitest run src/components/ai/__tests__/LinkedAssetControl.test.tsx ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（2 tests）。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/ai/LinkedAssetControl.tsx frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx
git commit -m "✨ LinkedAssetControl：绑定 chip + AssetSelect 换绑 + 清除"
```

---

### Task 7: @mention 首次提及 → 自动绑定

**Files:**
- Create: `frontend/src/lib/aiLinkedAsset.ts`
- Create: `frontend/src/lib/__tests__/aiLinkedAsset.test.ts`
- Modify: `frontend/src/stores/aiStore.ts` (`sendFromSidebarTab` `:2157-2199`)
- Test: `frontend/src/__tests__/aiStore.linkedAsset.test.ts`（追加）

**Interfaces:**
- Consumes: `extractMentions(content): MentionAttrs[]`（`@/lib/mentionXml`）、`setSidebarTabAsset`（T2）
- Produces: `export function linkedAssetFromMention(m: MentionAttrs): { assetId: number; assetName: string; assetType: string }`
- 行为：`sendFromSidebarTab` 里，若该 tab `linkedAssetId == null` 且 `extractMentions(content)` 非空 → 用**首个** mention 绑定。

- [ ] **Step 1: 写 helper 失败测试**

Create `frontend/src/lib/__tests__/aiLinkedAsset.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { linkedAssetFromMention } from "../aiLinkedAsset";

describe("linkedAssetFromMention", () => {
  it("maps mention attrs to linked asset meta", () => {
    expect(linkedAssetFromMention({ assetId: 5, name: "prod-web-01", type: "ssh" })).toEqual({
      assetId: 5, assetName: "prod-web-01", assetType: "ssh",
    });
  });
  it("defaults type to empty string when absent", () => {
    expect(linkedAssetFromMention({ assetId: 5, name: "x" }).assetType).toBe("");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/aiLinkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — 模块不存在。

- [ ] **Step 3: 实现 helper**

Create `frontend/src/lib/aiLinkedAsset.ts`:

```ts
import type { MentionAttrs } from "./mentionXml";

export function linkedAssetFromMention(m: MentionAttrs): { assetId: number; assetName: string; assetType: string } {
  return { assetId: m.assetId, assetName: m.name, assetType: m.type ?? "" };
}
```

- [ ] **Step 4: helper 测试通过**

```bash
cd frontend && ( pnpm exec vitest run src/lib/__tests__/aiLinkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（2 tests）。

- [ ] **Step 5: 写 store 自动绑定失败测试**（追加到 aiStore.linkedAsset.test.ts）

```ts
import { buildMentionXml } from "../lib/mentionXml";

describe("sendFromSidebarTab auto-binds first mention when unbound", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true, modelName: "gpt-4",
      conversationMessages: { 1: [] },
      conversationStreaming: { 1: { sending: false, pendingQueue: [] } },
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
      activeSidebarTabId: "s1",
    });
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
  });

  it("sets linkedAssetId from the first mention", async () => {
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} 帮我看下`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(7);
  });

  it("does not override an existing binding", async () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 99, assetName: "cache", assetType: "redis" });
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} x`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(99);
  });
});
```

- [ ] **Step 6: 跑测试确认失败**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: FAIL — 未自动绑定。

- [ ] **Step 7: 实现自动绑定**

In `aiStore.ts` 顶部 import：

```ts
import { extractMentions } from "@/lib/mentionXml";
import { linkedAssetFromMention } from "@/lib/aiLinkedAsset";
```

In `sendFromSidebarTab`（`:2157`），在 `if (!content.trim() && existingMessages.length === 0) return;` 之后、创建 conversation 之前插入：

```ts
      // 未绑定会话首次 @资产 → 自动设为主资产（不覆盖已有绑定）。
      if (sidebarTab.linkedAssetId == null && content.trim()) {
        const mentions = extractMentions(content);
        if (mentions.length > 0) {
          get().setSidebarTabAsset(tabId, linkedAssetFromMention(mentions[0]));
        }
      }
```

- [ ] **Step 8: 跑测试确认通过**

```bash
cd frontend && ( pnpm exec vitest run src/__tests__/aiStore.linkedAsset.test.ts src/lib/__tests__/aiLinkedAsset.test.ts ) ; git checkout -- pnpm-lock.yaml
```
Expected: PASS（helper 2 + store 8 = 10 tests）。

- [ ] **Step 9: 全量前端测试 + 类型检查（回归）**

```bash
cd frontend && ( pnpm exec tsc --noEmit && pnpm test ) ; git checkout -- pnpm-lock.yaml
```
Expected: tsc 0 error；全量测试全绿。

- [ ] **Step 10: 提交**

```bash
git add frontend/src/lib/aiLinkedAsset.ts frontend/src/lib/__tests__/aiLinkedAsset.test.ts frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.linkedAsset.test.ts
git commit -m "✨ 未绑定会话首个 @mention 自动绑定为主资产"
```

---

## Self-Review

**Spec coverage（对 `2026-07-09-ai-follow-terminal-design.md`）：**
- §4.1 默认未绑定 + 手动/@mention 建立 → T1（字段默认 undefined）、T2（手动 action）、T6（AssetSelect 手动）、T7（@mention 自动）。✓
- §4.2 可折叠、默认收起、标题行 + `关联资产 ⌄` → T5。✓
- §4.4 绑定资产进入上下文（Plan 1 用置顶+保证存在近似「标 active」；后端 `active` 字段 = Plan 2）→ T3。✓（近似）
- §4.2 绑定 chip + `▾`（换绑/清除）→ T6。✓（跟随开关在 `▾` 菜单 = Plan 2）
- §4.6 交互卡 ①④⑦⑧ 对应本 plan 能力；②③⑤⑥（跟随态/重绑/引用跳转/会话轨）= Plan 2。✓（已在 deferred 列明）

**Placeholder scan：** 每步含真实代码/命令/期望；无 TBD。T5→T6 的 `LinkedAssetControl` 占位在同 plan 内被 T6 替换，非跨 plan 悬空。✓

**Type consistency：** `setSidebarTabAsset(tabId, {assetId,assetName,assetType})` 在 T2 定义、T6/T7 一致调用；`linkedAssetFromMention` 返回同形状对象直接喂给它。`SidebarAITab.linkedAssetId?: number | null` 全程一致。`AssetSelect` 用 `value:number`/`onValueChange:(id:number)=>void`（与源码一致）。⚠ **实现前须核对** `useAssetStore` 资产字段名（测试假设 `ID/Name/Type/Icon`）与 `getAsset` 签名——T6 Step 1 已注明按实际调整。

**已知近似/风险：**
- T3 用「置顶 openTabs」近似上下文优先级；真正的 `active`/prompt 强调留 Plan 2（需 Go + 重新生成 wailsjs）。
- ContextBar 只拿 `conversationId`；T5 新增 `sidebarTabId` prop 由 `SideAssistantPanel` 传入（promote 到 workspace tab 的场景，绑定/展开由 sidebar 宿主承载，Plan 1 不覆盖 workspace-tab 独立绑定）。
