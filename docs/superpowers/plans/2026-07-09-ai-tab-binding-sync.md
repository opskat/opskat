# AI 会话「绑定 tab + 联动开关双向同步」实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 AI 会话的「跟随此终端」(随激活 tab 改绑资产) 重做为「绑定 tab 实例 + 联动开关双向导航同步」,并让下拉/会话列表的图标颜色复用 canonical helper。

**Architecture:** 前端 Zustand + React,IPC-only。绑定挂在 `SidebarAITab` 上:`linkedTabId`(工作区 tab 实例 id,导航联动)+ 保留派生 `linkedAsset*`(AI 上下文 + 显示)。联动开关 `syncTab` 闸控双向同步——方向 A 用 `tabStore.activeTabId` 订阅,方向 B 内嵌在 `activateSidebarTab` 动作;共享 `syncingTabBinding` 模块级 guard 防回环。图标经新增 `resolveAssetIcon(assets, assetId, type)` 统一走 `getIconComponent`/`getIconColor`。

**Tech Stack:** React 19, Zustand, Radix DropdownMenu, `@opskat/ui`, lucide-react, vitest + @testing-library/react。

**设计来源:** `docs/superpowers/specs/2026-07-09-ai-tab-binding-sync-design.md`(修订 v2,supersede 原稿跟随语义)。

## Global Constraints

- **TDD**:每个行为先写失败测试,再最小实现。测试命令 `pnpm test <file>`(= `vitest run <file>`)。
- **Reuse first**:图标一律 `getIconComponent`/`getIconColor`(`@/components/asset/IconPicker`)+ `getAssetType`(`@/lib/assetTypes`),勿另起。
- **无 shim / 无退休数据兼容**:直接删 `followActiveTerminal`,不留 `_renamed`/legacy 分支。
- **Toast**:成功走 `notify.ts`,错误/信息用 `toast.*`(本计划移除 followSwitched toast,不新增)。
- **提交**:gitmoji 前缀;本分支实现 commit subject 不带 `#160`(与现有分支历史一致,issue 在 PR 关联)。
- **i18n**:`zh-CN` + `en` 同步;各语言用地道表达,勿逐字对齐。
- 分支:沿用 `feature/ai-follow-terminal`(off main)。

---

### Task 1: 会话绑定 API（数据模型 + bind/unbind/setSync 动作）

把 `SidebarAITab` 的 `followActiveTerminal` 换成 `linkedTabId` + `syncTab`,并用 `bindSidebarTab`/`unbindSidebarTab`/`setSidebarTabSync` 替换 `setSidebarTabAsset`/`clearSidebarTabAsset`/`setSidebarTabFollow`。

**Files:**
- Modify: `frontend/src/stores/aiStore.ts`(interface `160-170`、`1701-1703`;`createSidebarTab` `232-244`;`sanitizeSidebarTab` `269-286`;actions `1937-1973`;`didSidebarStructureChange` `2415-2430`)
- Create: `frontend/src/__tests__/aiStore.binding.test.ts`
- Delete: `frontend/src/__tests__/aiStore.follow.test.ts`

**Interfaces:**
- Produces:
  - `SidebarAITab` 增 `linkedTabId?: string | null`、`syncTab?: boolean`,删 `followActiveTerminal`。
  - `bindSidebarTab(sidebarTabId: string, binding: { workspaceTabId: string | null; assetId: number; assetName: string; assetType: string }): void`
  - `unbindSidebarTab(sidebarTabId: string): void`
  - `setSidebarTabSync(sidebarTabId: string, on: boolean): void`

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/__tests__/aiStore.binding.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";

const mkTab = (over: Partial<Record<string, unknown>> = {}) => ({
  id: over.id ?? "s1",
  conversationId: over.conversationId ?? 1,
  title: "t",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  linkedTabId: over.linkedTabId,
  linkedAssetId: over.linkedAssetId,
  linkedAssetName: over.linkedAssetName,
  linkedAssetType: over.linkedAssetType,
  syncTab: over.syncTab,
});

describe("bindSidebarTab", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1" }) as any], activeSidebarTabId: "s1" });
  });

  it("binds a workspace tab + derived asset to the conversation", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1", assetId: 5, assetName: "web", assetType: "ssh" });
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBe("t1");
    expect(tab?.linkedAssetId).toBe(5);
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("does not auto-enable sync on bind", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1", assetId: 5, assetName: "web", assetType: "ssh" });
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBeFalsy();
  });

  it("1:1 exclusive — binding a tab already bound by another conversation steals it", () => {
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }) as any,
        mkTab({ id: "s2" }) as any,
      ],
    });
    useAIStore.getState().bindSidebarTab("s2", { workspaceTabId: "t1", assetId: 5, assetName: "web", assetType: "ssh" });
    const s1 = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    const s2 = useAIStore.getState().sidebarTabs.find((x) => x.id === "s2");
    expect(s1?.linkedTabId).toBeNull();        // stolen
    expect(s1?.syncTab).toBe(false);           // sync disabled when tab link lost
    expect(s1?.linkedAssetId).toBe(5);         // asset context preserved
    expect(s2?.linkedTabId).toBe("t1");
  });

  it("library binding (no open tab) sets asset only, leaves linkedTabId null", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: null, assetId: 7, assetName: "db", assetType: "mysql" });
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBeNull();
    expect(tab?.linkedAssetId).toBe(7);
  });
});

describe("unbindSidebarTab / setSidebarTabSync", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, linkedAssetType: "ssh", syncTab: true }) as any],
      activeSidebarTabId: "s1",
    });
  });

  it("unbind clears tab link, asset, and sync", () => {
    useAIStore.getState().unbindSidebarTab("s1");
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBeFalsy();
    expect(tab?.linkedAssetId).toBeUndefined();
    expect(tab?.syncTab).toBe(false);
  });

  it("setSidebarTabSync(true) enables sync when a tab is linked", () => {
    useAIStore.getState().setSidebarTabSync("s1", true);
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBe(true);
  });

  it("setSidebarTabSync(true) is a no-op with no linked tab (asset-only binding)", () => {
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1", linkedTabId: null, linkedAssetId: 5 }) as any] });
    useAIStore.getState().setSidebarTabSync("s1", true);
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBe(false);
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm test src/__tests__/aiStore.binding.test.ts`
Expected: FAIL —— `bindSidebarTab is not a function`。

- [ ] **Step 3: 改数据模型 + 动作**

3a. interface（`aiStore.ts:166-169`,`SidebarAITab` 内）——替换这四行:
```ts
  linkedAssetId?: number | null;
  linkedAssetName?: string;
  linkedAssetType?: string;
  followActiveTerminal?: boolean;
```
为:
```ts
  linkedTabId?: string | null;
  linkedAssetId?: number | null;
  linkedAssetName?: string;
  linkedAssetType?: string;
  syncTab?: boolean;
```

3b. action 签名（`aiStore.ts:1701-1703`）——替换:
```ts
  setSidebarTabAsset: (tabId: string, asset: { assetId: number; assetName: string; assetType: string }) => void;
  clearSidebarTabAsset: (tabId: string) => void;
  setSidebarTabFollow: (tabId: string, on: boolean) => void;
```
为:
```ts
  bindSidebarTab: (
    sidebarTabId: string,
    binding: { workspaceTabId: string | null; assetId: number; assetName: string; assetType: string }
  ) => void;
  unbindSidebarTab: (sidebarTabId: string) => void;
  setSidebarTabSync: (sidebarTabId: string, on: boolean) => void;
```

3c. `createSidebarTab`（`aiStore.ts:239-242`）——替换:
```ts
    linkedAssetId: typeof overrides?.linkedAssetId === "number" ? overrides.linkedAssetId : undefined,
    linkedAssetName: overrides?.linkedAssetName,
    linkedAssetType: overrides?.linkedAssetType,
    followActiveTerminal: overrides?.followActiveTerminal,
```
为:
```ts
    linkedTabId: typeof overrides?.linkedTabId === "string" ? overrides.linkedTabId : undefined,
    linkedAssetId: typeof overrides?.linkedAssetId === "number" ? overrides.linkedAssetId : undefined,
    linkedAssetName: overrides?.linkedAssetName,
    linkedAssetType: overrides?.linkedAssetType,
    syncTab: overrides?.syncTab,
```

3d. `sanitizeSidebarTab`（`aiStore.ts:281-284`）——替换:
```ts
    linkedAssetId: typeof tab.linkedAssetId === "number" && Number.isFinite(tab.linkedAssetId) ? tab.linkedAssetId : undefined,
    linkedAssetName: typeof tab.linkedAssetName === "string" ? tab.linkedAssetName : undefined,
    linkedAssetType: typeof tab.linkedAssetType === "string" ? tab.linkedAssetType : undefined,
    followActiveTerminal: typeof tab.followActiveTerminal === "boolean" ? tab.followActiveTerminal : undefined,
```
为:
```ts
    linkedTabId: typeof tab.linkedTabId === "string" ? tab.linkedTabId : undefined,
    linkedAssetId: typeof tab.linkedAssetId === "number" && Number.isFinite(tab.linkedAssetId) ? tab.linkedAssetId : undefined,
    linkedAssetName: typeof tab.linkedAssetName === "string" ? tab.linkedAssetName : undefined,
    linkedAssetType: typeof tab.linkedAssetType === "string" ? tab.linkedAssetType : undefined,
    syncTab: typeof tab.syncTab === "boolean" ? tab.syncTab : undefined,
```

3e. 动作实现（`aiStore.ts:1937-1973`）——替换整段 `setSidebarTabAsset`/`clearSidebarTabAsset`/`setSidebarTabFollow` 为:
```ts
    bindSidebarTab: (sidebarTabId, binding) => {
      set((state) => ({
        sidebarTabs: state.sidebarTabs.map((tab) => {
          // 1:1 独占：其它绑到同一 workspace tab 的会话让位（保留其资产上下文，仅断 tab 链 + 关联动）。
          if (binding.workspaceTabId != null && tab.id !== sidebarTabId && tab.linkedTabId === binding.workspaceTabId) {
            return { ...tab, linkedTabId: null, syncTab: false };
          }
          if (tab.id === sidebarTabId) {
            return {
              ...tab,
              linkedTabId: binding.workspaceTabId,
              linkedAssetId: binding.assetId,
              linkedAssetName: binding.assetName,
              linkedAssetType: binding.assetType,
            };
          }
          return tab;
        }),
      }));
    },
    unbindSidebarTab: (sidebarTabId) => {
      set((state) => ({
        sidebarTabs: state.sidebarTabs.map((tab) =>
          tab.id === sidebarTabId
            ? {
                ...tab,
                linkedTabId: null,
                linkedAssetId: undefined,
                linkedAssetName: undefined,
                linkedAssetType: undefined,
                syncTab: false,
              }
            : tab
        ),
      }));
    },
    setSidebarTabSync: (sidebarTabId, on) => {
      set((state) => ({
        sidebarTabs: state.sidebarTabs.map((tab) =>
          tab.id === sidebarTabId
            ? { ...tab, syncTab: on && tab.linkedTabId != null }  // 无 tab 链不可联动
            : tab
        ),
      }));
    },
```

3f. `didSidebarStructureChange`（`aiStore.ts:2424-2425`）——替换:
```ts
      a.linkedAssetId !== b.linkedAssetId ||
      a.followActiveTerminal !== b.followActiveTerminal
```
为:
```ts
      a.linkedAssetId !== b.linkedAssetId ||
      a.linkedTabId !== b.linkedTabId ||
      a.syncTab !== b.syncTab
```

> 注：此时 `LinkedAssetControl.tsx` / 订阅块仍引用旧名(`setSidebarTabFollow` 等),整包 `tsc` 尚不绿——Task 2/5 收尾。vitest 单文件不做全量类型检查,本任务测试可独立通过。

- [ ] **Step 4: 运行测试确认通过**

Run: `pnpm test src/__tests__/aiStore.binding.test.ts`
Expected: PASS(8 通过)。

- [ ] **Step 5: 删旧测试并提交**

```bash
git rm frontend/src/__tests__/aiStore.follow.test.ts
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.binding.test.ts
git commit -m "♻️ 会话绑定改为 tab 实例:linkedTabId + syncTab + bind/unbind/setSync"
```

---

### Task 2: 双向联动引擎（方向 A 订阅 + 方向 B 内嵌 activateSidebarTab）

用 `syncTab` 闸控:切工作区 tab → 激活绑定会话(方向 A);激活会话 → 激活绑定 tab(方向 B)。共享 `syncingTabBinding` guard 防回环。移除旧的「随激活 tab 改绑资产」订阅与 `followSwitched` toast。

**Files:**
- Modify: `frontend/src/stores/aiStore.ts`(顶部加 guard 变量;`activateSidebarTab` `1932-1935`;订阅块 `2450-2465`)
- Modify: `frontend/src/__tests__/aiStore.binding.test.ts`(追加 describe)

**Interfaces:**
- Consumes: Task 1 的 `SidebarAITab.syncTab`/`linkedTabId`、`bindSidebarTab`。
- Produces: `activateSidebarTab` 现在附带方向 B(激活 synced 会话→激活其 `linkedTabId` tab);模块级订阅实现方向 A。

- [ ] **Step 1: 追加失败测试**

在 `aiStore.binding.test.ts` 顶部 import 补一行:
```ts
import { useTabStore } from "../stores/tabStore";
```
文件末尾追加:
```ts
describe("bidirectional tab↔conversation sync", () => {
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
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, linkedAssetType: "ssh", syncTab: true }) as any,
        mkTab({ id: "s2", linkedTabId: "t2", linkedAssetId: 9, linkedAssetType: "ssh", syncTab: true }) as any,
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("A: switching workspace tab activates the synced conversation bound to it", () => {
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s2");
  });

  it("B: activating a synced conversation activates its bound workspace tab", () => {
    useAIStore.getState().activateSidebarTab("s2");
    expect(useTabStore.getState().activeTabId).toBe("t2");
  });

  it("does not activate a conversation whose sync is off", () => {
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }) as any,
        mkTab({ id: "s2", linkedTabId: "t2", linkedAssetId: 9, syncTab: false }) as any,
      ],
      activeSidebarTabId: "s1",
    });
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s1"); // unchanged
  });

  it("B: activating a synced conversation whose bound tab is gone leaves the active tab unchanged", () => {
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s3", linkedTabId: "closed", linkedAssetId: 5, syncTab: true }) as any],
      activeSidebarTabId: "s1",
    });
    useTabStore.setState({ activeTabId: "t1" });
    useAIStore.getState().activateSidebarTab("s3");
    expect(useTabStore.getState().activeTabId).toBe("t1"); // unchanged
  });

  it("does not infinite-loop between the two directions", () => {
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s2");
    expect(useTabStore.getState().activeTabId).toBe("t2");
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test src/__tests__/aiStore.binding.test.ts`
Expected: FAIL —— 方向 A/B 断言不成立(如 `activeSidebarTabId` 仍是 s1)。

- [ ] **Step 3a: 声明共享 guard(模块级,store 创建之前)**

在 `aiStore.ts` `export const useAIStore = create(...)` 之前(紧接 helper 定义,约 `230` 附近)加:
```ts
// 联动方向 A/B 共享的重入 guard —— 防止 tab↔会话互相激活形成回环。
let syncingTabBinding = false;
```

- [ ] **Step 3b: 方向 B 内嵌 `activateSidebarTab`（`aiStore.ts:1932-1935`）**

替换:
```ts
    activateSidebarTab: (tabId: string) => {
      if (!get().sidebarTabs.some((tab) => tab.id === tabId)) return;
      set({ activeSidebarTabId: tabId });
    },
```
为:
```ts
    activateSidebarTab: (tabId: string) => {
      if (!get().sidebarTabs.some((tab) => tab.id === tabId)) return;
      set({ activeSidebarTabId: tabId });
      // 联动方向 B：激活开了联动的会话 → 激活其绑定的工作区 tab（tab 已关则不动）。
      if (syncingTabBinding) return;
      const tab = get().sidebarTabs.find((t) => t.id === tabId);
      if (!tab?.syncTab || !tab.linkedTabId) return;
      const ts = useTabStore.getState();
      if (ts.activeTabId === tab.linkedTabId || !ts.tabs.some((t) => t.id === tab.linkedTabId)) return;
      syncingTabBinding = true;
      try {
        ts.activateTab(tab.linkedTabId);
      } finally {
        syncingTabBinding = false;
      }
    },
```

- [ ] **Step 3c: 方向 A 订阅（替换 `aiStore.ts:2450-2465` 整段）**

替换旧的「跟随开关」订阅为:
```ts
// 联动方向 A：激活工作区 tab 变化 → 激活绑定它且开了联动的会话。
let __lastActiveTabId: string | null = useTabStore.getState().activeTabId;
useTabStore.subscribe((state) => {
  if (state.activeTabId === __lastActiveTabId) return;
  __lastActiveTabId = state.activeTabId;
  if (syncingTabBinding) return;
  const store = useAIStore.getState();
  const target = store.sidebarTabs.find((t) => t.syncTab === true && t.linkedTabId === state.activeTabId);
  if (!target || target.id === store.activeSidebarTabId) return;
  syncingTabBinding = true;
  try {
    store.activateSidebarTab(target.id);
  } finally {
    syncingTabBinding = false;
  }
});
```

> `toast` / `i18n` 若因此变为未使用,按 lint 提示删除对应 import(见 Step 4 的 `pnpm lint`)。`followSwitched` i18n key 在 Task 5 一并删除。

- [ ] **Step 4: 运行确认通过 + lint**

Run: `pnpm test src/__tests__/aiStore.binding.test.ts`
Expected: PASS(13 通过)。
Run: `pnpm lint`(仅确认 aiStore.ts 无未使用 import 报错;`LinkedAssetControl.tsx` 的旧引用报错留待 Task 5)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.binding.test.ts
git commit -m "✨ AI 会话↔tab 双向联动:syncTab 闸控 + 防回环 guard"
```

---

### Task 3: 重连换 id 时迁移绑定（replaceHook）

终端重连时 tab id 变(`connectionId→sessionId`),注册 `registerTabReplaceHook` 把 `linkedTabId` 一并迁移,使绑定跨重连/重启存活。

**Files:**
- Modify: `frontend/src/stores/aiStore.ts`(import `23-24` 加 `registerTabReplaceHook`;方向 A 订阅之后加 hook)
- Modify: `frontend/src/__tests__/aiStore.binding.test.ts`(追加 describe)

**Interfaces:**
- Consumes: `registerTabReplaceHook(hook: (oldId: string, newId: string) => void)`(`tabStore.ts:76`)、Task 1 的 `linkedTabId`。

- [ ] **Step 1: 追加失败测试**

在 `aiStore.binding.test.ts` 末尾追加:
```ts
import { registerTabReplaceHook } from "../stores/tabStore";

describe("linkedTabId migrates on tab id replace (reconnect)", () => {
  it("moves linkedTabId when a bound tab's id is replaced", () => {
    localStorage.clear();
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "old-conn", linkedAssetId: 5, syncTab: true }) as any],
      activeSidebarTabId: "s1",
    });
    // aiStore 模块加载时已 registerTabReplaceHook；直接触发一次 replace 语义：
    useTabStore.getState().replaceTabId?.("old-conn", "new-session");
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("new-session");
  });
});
```
> `replaceTabId` 需 tabStore 有对应 tab 才改自身 tabs,但 hook 只依据 old/new 迁移会话侧,不依赖 tabStore 里是否存在该 tab —— 上面直接调 `replaceTabId` 会触发已注册的 hooks。

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test src/__tests__/aiStore.binding.test.ts`
Expected: FAIL —— `linkedTabId` 仍是 `"old-conn"`(hook 未注册)。

- [ ] **Step 3: 加 import + 注册 hook**

3a. import（`aiStore.ts:23-24`,`registerTabCloseHook,` 之后加一行）:
```ts
  registerTabCloseHook,
  registerTabReplaceHook,
  registerTabRestoreHook,
```

3b. 在 Task 2 的方向 A 订阅块之后追加:
```ts
// 终端重连换 id（connectionId→sessionId）时迁移绑定，使 linkedTabId 跨重连/重启存活。
registerTabReplaceHook((oldId, newId) => {
  const store = useAIStore.getState();
  let changed = false;
  const next = store.sidebarTabs.map((tab) => {
    if (tab.linkedTabId === oldId) {
      changed = true;
      return { ...tab, linkedTabId: newId };
    }
    return tab;
  });
  if (changed) useAIStore.setState({ sidebarTabs: next });
});
```

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test src/__tests__/aiStore.binding.test.ts`
Expected: PASS(14 通过)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/stores/aiStore.ts frontend/src/__tests__/aiStore.binding.test.ts
git commit -m "✨ 重连换 id 时迁移 linkedTabId,绑定跨重连存活"
```

---

### Task 4: `resolveAssetIcon` 共享图标解析器

统一给 AI 三处(chip / 已打开终端列表 / 会话头像)解析资产图标 + 颜色,复用 `getIconComponent`/`getIconColor`,资产不在 store 才回退类型默认图标。

**Files:**
- Create: `frontend/src/lib/aiAssetIcon.ts`
- Create: `frontend/src/lib/__tests__/aiAssetIcon.test.ts`

**Interfaces:**
- Produces:
  - `type AssetIconComponent = ComponentType<{ className?: string; style?: CSSProperties }>`
  - `interface ResolvedAssetIcon { Icon: AssetIconComponent; color: string | undefined }`
  - `resolveAssetIcon(assets: asset_entity.Asset[], assetId: number | null | undefined, fallbackType?: string): ResolvedAssetIcon`

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/lib/__tests__/aiAssetIcon.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { Server } from "lucide-react";
import { resolveAssetIcon } from "../aiAssetIcon";
import { getIconComponent } from "@/components/asset/IconPicker";
import { getAssetType } from "@/lib/assetTypes";

const assets = [
  { ID: 1, Name: "web", Type: "ssh", Icon: "server#ff0000" } as any,
  { ID: 2, Name: "db", Type: "mysql", Icon: "" } as any,
];

describe("resolveAssetIcon", () => {
  it("uses the asset's own Icon + color when present", () => {
    const r = resolveAssetIcon(assets, 1, "ssh");
    expect(r.Icon).toBe(getIconComponent("server#ff0000"));
    expect(r.color).toBe("#ff0000");
  });

  it("falls back to the asset-type icon (no color) when the asset has no Icon", () => {
    const r = resolveAssetIcon(assets, 2, "mysql");
    expect(r.Icon).toBe(getAssetType("mysql")?.icon);
    expect(r.color).toBeUndefined();
  });

  it("falls back to Server when the asset is not in the store", () => {
    const r = resolveAssetIcon(assets, 999, "unknown-type");
    expect(r.Icon).toBe(Server);
    expect(r.color).toBeUndefined();
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test src/lib/__tests__/aiAssetIcon.test.ts`
Expected: FAIL —— 找不到模块 `../aiAssetIcon`。

- [ ] **Step 3: 实现**

新建 `frontend/src/lib/aiAssetIcon.ts`:
```ts
import type { ComponentType, CSSProperties } from "react";
import { Server } from "lucide-react";
import { getIconComponent, getIconColor } from "@/components/asset/IconPicker";
import { getAssetType } from "@/lib/assetTypes";
import type { asset_entity } from "../../wailsjs/go/models";

export type AssetIconComponent = ComponentType<{ className?: string; style?: CSSProperties }>;

export interface ResolvedAssetIcon {
  Icon: AssetIconComponent;
  color: string | undefined;
}

/**
 * 统一解析资产图标 + 颜色,复用 canonical helper:
 * 优先用资产自身的 `Icon` 字段(getIconComponent/getIconColor);
 * 资产不在 store(如已删除)才回退到资产类型默认图标(getAssetType),无颜色。
 * 供 AI 的绑定 chip / 已打开终端列表 / 会话头像共用,避免各处重复实现。
 */
export function resolveAssetIcon(
  assets: asset_entity.Asset[],
  assetId: number | null | undefined,
  fallbackType?: string
): ResolvedAssetIcon {
  const asset = assetId != null ? assets.find((a) => a.ID === assetId) : undefined;
  if (asset?.Icon) {
    return { Icon: getIconComponent(asset.Icon) as AssetIconComponent, color: getIconColor(asset.Icon) };
  }
  const typeIcon = fallbackType ? getAssetType(fallbackType)?.icon : undefined;
  return { Icon: (typeIcon ?? Server) as AssetIconComponent, color: undefined };
}
```

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test src/lib/__tests__/aiAssetIcon.test.ts`
Expected: PASS(3 通过)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/lib/aiAssetIcon.ts frontend/src/lib/__tests__/aiAssetIcon.test.ts
git commit -m "✨ resolveAssetIcon:AI 侧统一资产图标/颜色解析(复用 IconPicker helper)"
```

---

### Task 5: `LinkedAssetControl` 重写（绑 tab + 联动开关 + 图标复用）+ i18n

绑定改为绑 tab 实例;下拉「已打开终端」带 tabId;联动开关 `disabled` 直到有活 tab 绑定;chip / 列表图标走 `resolveAssetIcon`;文案改「联动此 tab / 联动中」。

**Files:**
- Modify（整文件替换）: `frontend/src/components/ai/LinkedAssetControl.tsx`
- Modify: `frontend/src/i18n/locales/zh-CN/common.json:1046-1056`、`frontend/src/i18n/locales/en/common.json:1046-1056`
- Create: `frontend/src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`
- Delete: `frontend/src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx`

**Interfaces:**
- Consumes: Task 1 `bindSidebarTab`/`unbindSidebarTab`/`setSidebarTabSync`、Task 4 `resolveAssetIcon`、`tabToAssetRef`。

- [ ] **Step 1: 写失败测试**

新建 `frontend/src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`:
```tsx
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: () => <div data-testid="asset-select" />,
}));

/** Radix DropdownMenuTrigger opens on pointerdown(button=0). */
function openMenu(trigger: HTMLElement) {
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
}

const boundTab = {
  id: "s1",
  conversationId: 1,
  title: "t",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  linkedTabId: "t1",
  linkedAssetId: 42,
  linkedAssetName: "prod-web-01",
  linkedAssetType: "ssh",
};

describe("LinkedAssetControl binding + sync menu", () => {
  beforeEach(() => {
    useTabStore.setState({
      tabs: [{ id: "t1", type: "terminal", label: "prod-web-01", meta: { assetId: 42, assetName: "prod-web-01" } } as any],
      activeTabId: "t1",
    });
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({ sidebarTabs: [boundTab as any], activeSidebarTabId: "s1" });
  });

  it("binds a workspace tab from the open-terminals list", () => {
    useAIStore.setState({
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } } as any],
      activeSidebarTabId: "s1",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-terminal-42"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("t1");
    expect(tab?.linkedAssetId).toBe(42);
  });

  it("toggles sync via the dropdown when a live tab is bound", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-sync"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.syncTab).toBe(true);
    expect(screen.getByTestId("linked-asset-menu-trigger")).toHaveAttribute("title", "ai.sidebar.syncing");
  });

  it("clears the binding from the menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-clear"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBeUndefined();
  });

  it("disables sync when the bound tab is no longer open (asset context kept)", () => {
    useTabStore.setState({ tabs: [], activeTabId: null }); // bound tab closed
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-sync"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.syncTab).toBeFalsy();
    // 资产仍在(上下文保留)
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBe(42);
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`
Expected: FAIL —— 无 `menu-terminal-42`/`menu-sync` 或 `bindSidebarTab` 未接线。

- [ ] **Step 3: 整文件替换 `LinkedAssetControl.tsx`**

```tsx
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  cn,
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  Switch,
} from "@opskat/ui";
import { Check, ChevronDown, CircleDashed, Link2, Plus, X } from "lucide-react";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";
import { tabToAssetRef } from "@/lib/tabAsset";
import { resolveAssetIcon } from "@/lib/aiAssetIcon";

type OpenTerminal = { tabId: string; assetId: number; assetName: string; assetType: string };

/** 绑定控件:chip 触发的下拉(已打开终端 + 资产库 + 清除 + 联动开关)。绑定到 tab 实例,联动开关闸控双向导航同步。 */
export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const bindTab = useAIStore((s) => s.bindSidebarTab);
  const unbindTab = useAIStore((s) => s.unbindSidebarTab);
  const setSync = useAIStore((s) => s.setSidebarTabSync);
  const assets = useAssetStore((s) => s.assets);
  const tabs = useTabStore((s) => s.tabs);
  const [picking, setPicking] = useState(false);

  // 已打开的工作区 tab → 资产引用,按资产去重(同一资产多开只列第一个 tab)。
  const openTerminals = useMemo(() => {
    const seen = new Set<number>();
    const out: OpenTerminal[] = [];
    for (const wt of tabs) {
      const ref = tabToAssetRef(wt);
      if (!ref || seen.has(ref.assetId)) continue;
      seen.add(ref.assetId);
      out.push({ tabId: wt.id, ...ref });
    }
    return out;
  }, [tabs]);

  if (!sidebarTabId) return null;

  const bound = tab?.linkedAssetId != null;
  const tabLive = tab?.linkedTabId != null && tabs.some((wt) => wt.id === tab.linkedTabId);
  const syncing = !!tab?.syncTab;
  const syncTitle = syncing ? t("ai.sidebar.syncing") : undefined;
  const { Icon: BoundIcon, color: boundColor } = resolveAssetIcon(assets, tab?.linkedAssetId, tab?.linkedAssetType);

  const bindToTerminal = (term: OpenTerminal) =>
    bindTab(sidebarTabId, { workspaceTabId: term.tabId, assetId: term.assetId, assetName: term.assetName, assetType: term.assetType });

  // 资产库选择器只回传 id;名称/类型从 assetStore 补全。若该资产恰有打开的 tab 则一并绑 tab,否则仅绑资产(linkedTabId=null)。
  const handleLibraryPick = (assetId: number) => {
    const asset = assets.find((a) => a.ID === assetId);
    if (asset) {
      const open = openTerminals.find((o) => o.assetId === assetId);
      bindTab(sidebarTabId, { workspaceTabId: open?.tabId ?? null, assetId, assetName: asset.Name, assetType: asset.Type });
    }
    setPicking(false);
  };

  const triggerLabel = bound
    ? syncTitle
      ? `${tab?.linkedAssetName} · ${syncTitle}`
      : tab?.linkedAssetName || undefined
    : t("ai.sidebar.linkedAsset.pickPlaceholder");

  return (
    <div className="flex items-center gap-2" data-testid="linked-asset-chip">
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="linked-asset-menu-trigger"
            title={syncTitle}
            aria-label={triggerLabel}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs",
              bound
                ? "border-primary/40 bg-primary/10 text-foreground"
                : "border-border bg-muted/30 text-muted-foreground"
            )}
          >
            {bound ? (
              <>
                {syncing && <Link2 className="h-3 w-3 text-primary" />}
                <span className={cn("h-1.5 w-1.5 rounded-full", tabLive ? "bg-success" : "bg-muted-foreground/50")} />
                <BoundIcon className="h-3.5 w-3.5" style={boundColor ? { color: boundColor } : undefined} />
                <span className="max-w-[140px] truncate">{tab?.linkedAssetName}</span>
              </>
            ) : (
              <>
                <CircleDashed className="h-3.5 w-3.5" />
                <span className="max-w-[160px] truncate">{t("ai.sidebar.linkedAsset.pickPlaceholder")}</span>
              </>
            )}
            <ChevronDown className="h-3 w-3 opacity-60" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-[220px]" onCloseAutoFocus={(e) => e.preventDefault()}>
          {openTerminals.length > 0 && (
            <>
              <DropdownMenuLabel className="px-2 py-1 text-[11px] font-semibold text-muted-foreground/70">
                {t("ai.sidebar.linkedAsset.openTerminals")}
              </DropdownMenuLabel>
              {openTerminals.map((term) => {
                const { Icon, color } = resolveAssetIcon(assets, term.assetId, term.assetType);
                const isBound = term.tabId === tab?.linkedTabId;
                return (
                  <DropdownMenuItem
                    key={term.tabId}
                    data-testid={`menu-terminal-${term.assetId}`}
                    onSelect={() => bindToTerminal(term)}
                    className={cn("gap-2", isBound && "bg-primary/10")}
                  >
                    <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                    <Icon className="h-3.5 w-3.5" style={color ? { color } : undefined} />
                    <span className="flex-1 truncate">{term.assetName}</span>
                    {isBound && <Check className="h-3.5 w-3.5 text-primary" />}
                  </DropdownMenuItem>
                );
              })}
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuItem data-testid="menu-pick-library" onSelect={() => setPicking(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t("ai.sidebar.linkedAsset.pickFromLibrary")}
          </DropdownMenuItem>
          {bound && (
            <DropdownMenuItem data-testid="menu-clear" onSelect={() => unbindTab(sidebarTabId)}>
              <X className="h-3.5 w-3.5" />
              {t("ai.sidebar.linkedAsset.clearBinding")}
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            data-testid="menu-sync"
            disabled={!tabLive}
            onSelect={(e) => {
              e.preventDefault();
              if (tabLive) setSync(sidebarTabId, !syncing);
            }}
            className="gap-2"
          >
            <Link2 className="h-3.5 w-3.5" />
            <span className="flex-1">{t("ai.sidebar.sync")}</span>
            <Switch checked={syncing} aria-hidden tabIndex={-1} className="pointer-events-none" />
          </DropdownMenuItem>
          <div className="px-2 py-1 text-[11px] leading-4 text-muted-foreground/60">
            {bound && !tabLive ? t("ai.sidebar.linkedAsset.tabClosed") : t("ai.sidebar.linkedAsset.syncHint")}
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
      {picking && (
        <AssetSelect
          value={tab?.linkedAssetId ?? 0}
          onValueChange={handleLibraryPick}
          placeholder={t("ai.sidebar.linkedAsset.pickPlaceholder")}
          testId="linked-asset-picker"
        />
      )}
    </div>
  );
}
```

- [ ] **Step 4: 改 i18n（删 follow*，加 sync/tabClosed，改 hint）**

`frontend/src/i18n/locales/zh-CN/common.json:1046-1056` —— 替换:
```json
      "linkedAsset": {
        "pickPlaceholder": "选择要绑定的资产",
        "openTerminals": "已打开的终端 / 会话",
        "pickFromLibrary": "从资产库选择…",
        "clearBinding": "清除绑定",
        "followHint": "默认关闭 · 顶部只显示绑定 chip"
      },
      "followSwitched": "已跟随切换到 {{name}}",
      "follow": "跟随此终端",
      "following": "跟随中",
      "referencedThisSession": "本次引用"
```
为:
```json
      "linkedAsset": {
        "pickPlaceholder": "选择要绑定的资产",
        "openTerminals": "已打开的终端 / 会话",
        "pickFromLibrary": "从资产库选择…",
        "clearBinding": "清除绑定",
        "syncHint": "开启后：切到此 tab 自动切到本会话，切到本会话自动切到此 tab",
        "tabClosed": "绑定的 tab 已关闭 · 可重新绑定"
      },
      "sync": "联动此 tab",
      "syncing": "联动中",
      "referencedThisSession": "本次引用"
```

`frontend/src/i18n/locales/en/common.json:1046-1056` —— 替换:
```json
      "linkedAsset": {
        "pickPlaceholder": "Choose an asset to bind",
        "openTerminals": "Open terminals / sessions",
        "pickFromLibrary": "Choose from asset library…",
        "clearBinding": "Clear binding",
        "followHint": "Off by default · only the bound chip shows up top"
      },
      "followSwitched": "Following → {{name}}",
      "follow": "Follow this terminal",
      "following": "Following",
      "referencedThisSession": "Referenced"
```
为:
```json
      "linkedAsset": {
        "pickPlaceholder": "Choose an asset to bind",
        "openTerminals": "Open terminals / sessions",
        "pickFromLibrary": "Choose from asset library…",
        "clearBinding": "Clear binding",
        "syncHint": "When on: this tab activates this chat, and this chat activates the tab",
        "tabClosed": "The bound tab is closed · rebind to relink"
      },
      "sync": "Link to this tab",
      "syncing": "Linked",
      "referencedThisSession": "Referenced"
```

- [ ] **Step 5: 运行确认通过**

Run: `pnpm test src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`
Expected: PASS(4 通过)。

- [ ] **Step 6: 删旧测试并提交**

```bash
git rm frontend/src/components/ai/__tests__/LinkedAssetControl.follow.test.tsx
git add frontend/src/components/ai/LinkedAssetControl.tsx \
        frontend/src/i18n/locales/zh-CN/common.json \
        frontend/src/i18n/locales/en/common.json \
        frontend/src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx
git commit -m "✨ LinkedAssetControl 绑 tab 实例 + 联动开关 + 图标复用;文案改联动"
```

---

### Task 6: 会话列表头像走 `resolveAssetIcon`（点 ③）

`SideAssistantTabBar` 头像:绑定态用资产真实图标 + `getIconColor` 颜色(经 `resolveAssetIcon`);未绑定态维持标题首字 + 哈希底色。

**Files:**
- Modify: `frontend/src/components/ai/SideAssistantTabBar.tsx`(import `5`;`renderAvatarContent` `27-36`;派生 `101-103`;两处头像 `126-132`、`191-196`)
- Modify: `frontend/src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx`

**Interfaces:**
- Consumes: Task 4 `resolveAssetIcon` / `ResolvedAssetIcon`、`useAssetStore`。

- [ ] **Step 1: 追加/改测试**

整文件替换 `frontend/src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx`:
```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { SideAssistantTabBar } from "../SideAssistantTabBar";
import { useAssetStore } from "@/stores/assetStore";

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
  beforeEach(() => {
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server#22c55e" } as any] });
  });

  it("renders the bound asset icon when the tab is bound", () => {
    renderBar({ linkedAssetId: 42, linkedAssetType: "ssh", linkedAssetName: "prod-web-01" });
    expect(screen.getByTestId("session-asset-icon-s1")).toBeInTheDocument();
  });

  it("colors the bound avatar icon with the asset's own color", () => {
    renderBar({ linkedAssetId: 42, linkedAssetType: "ssh", linkedAssetName: "prod-web-01" });
    const svg = screen.getByTestId("session-asset-icon-s1").querySelector("svg");
    expect(svg?.getAttribute("style") ?? "").toContain("color");
  });

  it("falls back to the title letter when unbound", () => {
    renderBar({});
    expect(screen.queryByTestId("session-asset-icon-s1")).toBeNull();
    expect(screen.getByText("P")).toBeInTheDocument(); // getSessionIconLetter("prod-web-01") → "P"
  });
});
```
> 注:`toContain("color")` 只验证颜色被写入(避免 jsdom 对 hex/rgb 归一化的脆弱断言);颜色取值正确性由 Task 4 的 `resolveAssetIcon` 单测覆盖。

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx`
Expected: FAIL —— 绑定态图标无 `style` 颜色(现用类型图标无色)。

- [ ] **Step 3a: import（`SideAssistantTabBar.tsx:5-6`）**

替换:
```ts
import { getAssetType, type AssetTypeDefinition } from "@/lib/assetTypes";
import { getSessionIconColor, getSessionIconLetter } from "./sessionIconColor";
```
为:
```ts
import { useAssetStore } from "@/stores/assetStore";
import { resolveAssetIcon, type ResolvedAssetIcon } from "@/lib/aiAssetIcon";
import { getSessionIconColor, getSessionIconLetter } from "./sessionIconColor";
```

- [ ] **Step 3b: `renderAvatarContent`（`27-36`）**

替换整个函数为:
```tsx
/** 头像内容:绑定资产 → 渲染资产图标(真实图标 + 颜色);否则回退标题首字。折叠/展开两处复用。 */
function renderAvatarContent(tabId: string, resolved: ResolvedAssetIcon | null, letter: string) {
  if (resolved) {
    const { Icon } = resolved;
    return (
      <span data-testid={`session-asset-icon-${tabId}`}>
        <Icon className="h-3.5 w-3.5" style={resolved.color ? { color: resolved.color } : undefined} />
      </span>
    );
  }
  return letter;
}
```

- [ ] **Step 3c: 组件内订阅 assets + 改派生（`101-103` 附近）**

在 `export function SideAssistantTabBar(...) {` 里、`const { t } = useTranslation();` 之后加:
```tsx
  const assets = useAssetStore((s) => s.assets);
```
把派生三行(`101-103`):
```tsx
            const letter = isBlank ? "?" : getSessionIconLetter(titleText);
            const color = isBlank ? null : getSessionIconColor(titleText);
            const AssetIcon = tab.linkedAssetType ? getAssetType(tab.linkedAssetType)?.icon : undefined;
```
替换为:
```tsx
            const letter = isBlank ? "?" : getSessionIconLetter(titleText);
            const bound = tab.linkedAssetId != null;
            const color = isBlank || bound ? null : getSessionIconColor(titleText);
            const resolved = bound ? resolveAssetIcon(assets, tab.linkedAssetId, tab.linkedAssetType) : null;
```

- [ ] **Step 3d: 折叠态头像（`126-132`）**

把 `className={cn(...)}` 里 `isBlank && "border border-dashed ..."` 那组不动,在 `style={color ? ... }` 之后的内容调用改为 `resolved`,并给绑定态加中性底色。具体:折叠态 `<button ...>` 的 className 追加 `bound && "bg-muted/70 text-foreground"`,把 `{renderAvatarContent(tab.id, AssetIcon, letter)}` 改成 `{renderAvatarContent(tab.id, resolved, letter)}`。替换该 button 的 className 块:
```tsx
                        className={cn(
                          "relative flex h-7 w-7 items-center justify-center rounded-md text-xs font-bold transition-transform hover:scale-105",
                          isActive && "ring-2 ring-primary ring-offset-1 ring-offset-sidebar",
                          isBlank && "border border-dashed border-muted-foreground/40 text-muted-foreground/70"
                        )}
```
为:
```tsx
                        className={cn(
                          "relative flex h-7 w-7 items-center justify-center rounded-md text-xs font-bold transition-transform hover:scale-105",
                          isActive && "ring-2 ring-primary ring-offset-1 ring-offset-sidebar",
                          bound && "bg-muted/70 text-foreground",
                          isBlank && "border border-dashed border-muted-foreground/40 text-muted-foreground/70"
                        )}
```
并把该 button 内 `{renderAvatarContent(tab.id, AssetIcon, letter)}` 改为 `{renderAvatarContent(tab.id, resolved, letter)}`。

- [ ] **Step 3e: 展开态头像（`189-196`）**

把展开态头像 `<span className={cn("relative flex h-7 w-7 ...")}>` 的 className 追加 `bound && "bg-muted/70 text-foreground"`:
```tsx
                  <span
                    className={cn(
                      "relative flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-xs font-bold",
                      bound && "bg-muted/70 text-foreground",
                      isBlank && "border border-dashed border-muted-foreground/40 text-muted-foreground/70"
                    )}
                    style={color ? { background: color.bg, color: color.fg } : undefined}
                  >
```
并把该 span 内 `{renderAvatarContent(tab.id, AssetIcon, letter)}` 改为 `{renderAvatarContent(tab.id, resolved, letter)}`。

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx`
Expected: PASS(3 通过)。

- [ ] **Step 5: 提交**

```bash
git add frontend/src/components/ai/SideAssistantTabBar.tsx \
        frontend/src/components/ai/__tests__/SideAssistantTabBar.avatar.test.tsx
git commit -m "💄 会话列表头像走 resolveAssetIcon:绑定态资产真实图标+颜色"
```

---

### Task 7: 全量验证 + 残留引用清理 + 应用内观察

**Files:**
- 可能 Modify: 任何仍引用旧符号(`followActiveTerminal`/`setSidebarTabFollow`/`ai.sidebar.follow`)的文件。

- [ ] **Step 1: 全仓 grep 残留旧符号**

Run:
```bash
cd frontend && grep -rn "followActiveTerminal\|setSidebarTabFollow\|setSidebarTabAsset\|clearSidebarTabAsset\|ai\.sidebar\.follow\|followSwitched\|linkedAsset\.followHint" src
```
Expected: 无输出(除本计划文档外)。若命中,按新符号(`syncTab`/`bindSidebarTab`/`unbindSidebarTab`/`setSidebarTabSync`/`ai.sidebar.sync`)修正并纳入下方提交。

- [ ] **Step 2: 全量测试**

Run: `pnpm test`
Expected: 全绿。若有其它引用 `SidebarAITab.followActiveTerminal` 的快照/测试失败,按新字段更新。

- [ ] **Step 3: lint**

Run: `pnpm lint`
Expected: 0 error。清理 aiStore.ts / SideAssistantTabBar.tsx 里因重写产生的未使用 import(如残留的 `toast`/`i18n`/`getAssetType`)。

- [ ] **Step 4: 应用内观察(verify skill)**

用 `/run` 或 `wails dev` 启动应用,人工验证四条:
1. 打开两个终端 tab(如 prod-web / cache),各新建 AI 会话,分别从下拉「已打开终端」绑定 → chip 显示资产真实图标+颜色,与资产树/tab 一致。
2. 在一个会话上开「联动此 tab」→ 切到该终端 tab,右侧 AI 自动切到该会话;在会话轨点另一个已联动会话 → 工作区自动切到其绑定 tab。
3. 关闭某绑定 tab → 该会话 chip 变灰(圆点转 muted)、联动开关置灰,但发送消息 AI 仍指向该资产。
4. 会话轨头像:绑定态显示资产图标+颜色,未绑定态显示标题首字。
观察 `logs/opskat.log` 无异常。

- [ ] **Step 5: 提交(若 Step 1/3 有清理)**

```bash
git add -A
git commit -m "✅ 清理旧跟随符号残留,全量测试/lint 通过"
```

---

## Self-Review

- **Spec coverage**:§2 绑定目标(Task 1 `linkedTabId`)、tab 关闭保留上下文(Task 1 asset 保留 + Task 5 chip 置灰 + `_sendForConversation` 未动)、1:1 独占(Task 1 steal)、联动开关双向(Task 2)、默认不绑(未自动绑,createSidebarTab 无默认)、replaceHook(Task 3)、图标复用(Task 4/5/6)、文案(Task 5)、测试(Task 1-6)。§10 扩展点逐条有 Task。✅
- **Placeholder scan**:无 TBD/TODO;每步含完整代码。✅
- **Type consistency**:`bindSidebarTab(sidebarTabId, {workspaceTabId, assetId, assetName, assetType})`、`syncTab`、`linkedTabId`、`resolveAssetIcon(assets, assetId, fallbackType)`、`ResolvedAssetIcon{Icon,color}` 在 Task 1/4/5/6 用法一致。✅
- **已知中间态**:Task 1 后整包 `tsc` 未绿(旧引用留待 Task 2/5),各任务 vitest 单文件独立通过;Task 7 做全量 `pnpm test` + `pnpm lint` 收口。
