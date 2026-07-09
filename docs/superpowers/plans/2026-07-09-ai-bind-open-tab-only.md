# AI 对话绑定「打开的标签页」实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `LinkedAssetControl` 的绑定对象从「资产」收窄为「打开的标签页」——删「从资产库选择」、绑定列表按 tab 逐个列出、文案全部 `资产→标签页`。

**Architecture:** 纯前端改动。组件层去掉资产库入口与按资产去重；i18n 两语言把 `资产` 文案改 `标签页`；store 与双向联动引擎不动。

**Tech Stack:** React 19 + Zustand + react-i18next + `@opskat/ui`（Radix dropdown）+ Vitest / Testing Library。

## Global Constraints

- 只改 `LinkedAssetControl.tsx` + 两语言 `common.json` + 两个 `LinkedAssetControl*.test.tsx`。**不改** `aiStore.ts`（含 `@mention` 自动绑定 `workspaceTabId:null`）、`tabStore.ts`、`SideAssistantContextBar.tsx` 逻辑。
- 绑定列表 **按 tab 逐个列**（不按资产去重）；列表项显示 `tab.label`；`data-testid` = `menu-tab-${tab.id}`（per-tab 唯一）。
- 文案全部 `资产→标签页`，含 `contextSection`/`contextExpand`/`contextCollapse`。删死键 `attachedAssets`、删 `linkedAsset.pickFromLibrary`、`openAssets`→`openTabs`、新增 `linkedAsset.noOpenTabs`。
- 成功 toast 走 `notify.ts`（本次无新增 toast）。测试文件沿用仓库惯例（`as any`）。`pnpm lint` 非门禁，只保证本次改动文件干净。
- 复用现有 `resolveAssetIcon` / `tabToAssetRef`，不新建图标或资产解析逻辑。

---

### Task 1: 收窄绑定为「打开的标签页」

**Files:**
- Modify: `frontend/src/components/ai/LinkedAssetControl.tsx`（整文件替换，见 Step 3）
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`（`ai.sidebar` 段，见 Step 4）
- Modify: `frontend/src/i18n/locales/en/common.json`（`ai.sidebar` 段，见 Step 4）
- Test: `frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx`（整文件替换，见 Step 1）
- Test: `frontend/src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`（整文件替换，见 Step 1）

**Interfaces:**
- Consumes: `useAIStore` 的 `bindSidebarTab(sidebarTabId, { workspaceTabId: string|null, assetId, assetName, assetType })`、`unbindSidebarTab(sidebarTabId)`、`setSidebarTabSync(sidebarTabId, on)`；`SidebarAITab` 字段 `linkedTabId?/linkedAssetId?/linkedAssetName?/linkedAssetType?/syncTab?`。`tabToAssetRef(tab) → {assetId, assetName, assetType}|null`。`resolveAssetIcon(assets, assetId?, fallbackType?) → {Icon, color?}`。`Tab` 有 `id/label/type/meta`。
- Produces: 无新导出（组件签名不变）。i18n 键：新增 `ai.sidebar.linkedAsset.openTabs`、`ai.sidebar.linkedAsset.noOpenTabs`；删除 `ai.sidebar.linkedAsset.openAssets`、`ai.sidebar.linkedAsset.pickFromLibrary`、`ai.sidebar.attachedAssets`。

- [ ] **Step 1: 用新行为改写两个测试（失败测试）**

`frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx` 整文件替换为：

```tsx
import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

/** Radix DropdownMenuTrigger opens on pointerdown (button=0) under happy-dom. */
function openMenu() {
  fireEvent.pointerDown(screen.getByTestId("linked-asset-menu-trigger"), { button: 0, ctrlKey: false });
}

describe("LinkedAssetControl", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
        },
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("unbound: chip shows the pick placeholder; no asset-library entry", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    expect(screen.getByTestId("linked-asset-menu-trigger")).toHaveTextContent("ai.sidebar.linkedAsset.pickPlaceholder");
    openMenu();
    expect(screen.queryByTestId("menu-pick-library")).not.toBeInTheDocument();
  });

  it("empty state: no open tabs → shows the noOpenTabs row and no tab items", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    expect(screen.getByTestId("menu-no-open-tabs")).toHaveTextContent("ai.sidebar.linkedAsset.noOpenTabs");
    expect(screen.queryByTestId(/^menu-tab-/)).not.toBeInTheDocument();
  });

  it("lists open tabs and binds the one clicked (keyed by tab id, shows tab label)", () => {
    useTabStore.setState({
      tabs: [
        {
          id: "t7",
          type: "terminal",
          label: "web · shell",
          meta: { type: "terminal", assetId: 7, assetName: "web-07" } as any,
        },
      ],
      activeTabId: "t7",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    expect(screen.getByTestId("menu-tab-t7")).toHaveTextContent("web · shell");
    fireEvent.click(screen.getByTestId("menu-tab-t7"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("t7");
    expect(tab?.linkedAssetId).toBe(7);
    expect(tab?.linkedAssetName).toBe("web-07");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("lists two tabs of the SAME asset as two separate items (no dedup by asset)", () => {
    useTabStore.setState({
      tabs: [
        { id: "t1", type: "terminal", label: "prod-web-01", meta: { type: "terminal", assetId: 7, assetName: "prod-web-01" } as any },
        { id: "t2", type: "terminal", label: "prod-web-01", meta: { type: "terminal", assetId: 7, assetName: "prod-web-01" } as any },
      ],
      activeTabId: "t1",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    expect(screen.getByTestId("menu-tab-t1")).toBeInTheDocument();
    expect(screen.getByTestId("menu-tab-t2")).toBeInTheDocument();
  });

  it("shows the bound chip and clears on 清除绑定", () => {
    useAIStore
      .getState()
      .bindSidebarTab("s1", { workspaceTabId: null, assetId: 42, assetName: "prod-web-01", assetType: "ssh" });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    expect(screen.getByText("prod-web-01")).toBeInTheDocument();
    openMenu();
    fireEvent.click(screen.getByTestId("menu-clear"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
```

`frontend/src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx` 整文件替换为：

```tsx
import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

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
      tabs: [
        { id: "t1", type: "terminal", label: "prod-web-01", meta: { assetId: 42, assetName: "prod-web-01" } } as any,
      ],
      activeTabId: "t1",
    });
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({ sidebarTabs: [boundTab as any], activeSidebarTabId: "s1" });
  });

  it("binds a workspace tab from the open-tabs list", () => {
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
        } as any,
      ],
      activeSidebarTabId: "s1",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-tab-t1"));
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
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBe(42);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run（在 `frontend/` 下）: `pnpm test src/components/ai/__tests__/LinkedAssetControl.test.tsx src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`
Expected: FAIL —— 找不到 `menu-tab-t7`/`menu-tab-t1`/`menu-no-open-tabs`（组件仍渲染 `menu-terminal-*` 与 `menu-pick-library`）。

- [ ] **Step 3: 改写组件 `LinkedAssetControl.tsx`（整文件替换）**

```tsx
import { useMemo } from "react";
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
import { Check, ChevronDown, CircleDashed, Link2, X } from "lucide-react";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";
import { tabToAssetRef } from "@/lib/tabAsset";
import { resolveAssetIcon } from "@/lib/aiAssetIcon";

type OpenTab = { tabId: string; label: string; assetId: number; assetName: string; assetType: string };

/** 绑定控件:chip 触发的下拉(已打开的标签页列表 + 清除 + 联动开关)。绑定到 tab 实例,联动开关闸控双向导航同步。 */
export function LinkedAssetControl({ sidebarTabId }: { sidebarTabId: string | null }) {
  const { t } = useTranslation();
  const tab = useAIStore((s) => s.sidebarTabs.find((x) => x.id === sidebarTabId));
  const bindTab = useAIStore((s) => s.bindSidebarTab);
  const unbindTab = useAIStore((s) => s.unbindSidebarTab);
  const setSync = useAIStore((s) => s.setSidebarTabSync);
  const assets = useAssetStore((s) => s.assets);
  const tabs = useTabStore((s) => s.tabs);

  // 已打开的工作区 tab → 资产引用,按 tab 逐个列出(同一资产多开各列一项,靠 tab 标题区分)。
  const openTabs = useMemo(() => {
    const out: OpenTab[] = [];
    for (const wt of tabs) {
      const ref = tabToAssetRef(wt);
      if (!ref) continue;
      out.push({ tabId: wt.id, label: wt.label, ...ref });
    }
    return out;
  }, [tabs]);

  if (!sidebarTabId) return null;

  const bound = tab?.linkedAssetId != null;
  const tabLive = tab?.linkedTabId != null && tabs.some((wt) => wt.id === tab.linkedTabId);
  const syncing = !!tab?.syncTab;
  const syncTitle = syncing ? t("ai.sidebar.syncing") : undefined;
  const { Icon: BoundIcon, color: boundColor } = resolveAssetIcon(assets, tab?.linkedAssetId, tab?.linkedAssetType);

  const bindToTab = (item: OpenTab) =>
    bindTab(sidebarTabId, {
      workspaceTabId: item.tabId,
      assetId: item.assetId,
      assetName: item.assetName,
      assetType: item.assetType,
    });

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
          <DropdownMenuLabel className="px-2 py-1 text-[11px] font-semibold text-muted-foreground/70">
            {t("ai.sidebar.linkedAsset.openTabs")}
          </DropdownMenuLabel>
          {openTabs.length === 0 ? (
            <div data-testid="menu-no-open-tabs" className="px-2 py-1.5 text-xs text-muted-foreground/60">
              {t("ai.sidebar.linkedAsset.noOpenTabs")}
            </div>
          ) : (
            openTabs.map((item) => {
              const { Icon, color } = resolveAssetIcon(assets, item.assetId, item.assetType);
              const isBound = item.tabId === tab?.linkedTabId;
              return (
                <DropdownMenuItem
                  key={item.tabId}
                  data-testid={`menu-tab-${item.tabId}`}
                  onSelect={() => bindToTab(item)}
                  className={cn("gap-2", isBound && "bg-primary/10")}
                >
                  <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                  <Icon className="h-3.5 w-3.5" style={color ? { color } : undefined} />
                  <span className="flex-1 truncate">{item.label}</span>
                  {isBound && <Check className="h-3.5 w-3.5 text-primary" />}
                </DropdownMenuItem>
              );
            })
          )}
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
    </div>
  );
}
```

- [ ] **Step 4: 改 i18n 两语言（`ai.sidebar` 段）**

`frontend/src/i18n/locales/zh-CN/common.json`：
- 删除 `"attachedAssets": "关联资产",` 一行。
- `"contextSection": "关联资产"` → `"关联标签页"`
- `"contextExpand": "展开关联资产"` → `"展开关联标签页"`
- `"contextCollapse": "收起关联资产"` → `"收起关联标签页"`
- `linkedAsset` 对象内：
  - `"pickPlaceholder": "选择要绑定的资产"` → `"选择要绑定的标签页"`
  - `"openAssets": "已打开的资产"` → 改键名与值：`"openTabs": "已打开的标签页"`
  - 删除 `"pickFromLibrary": "从资产库选择…",`
  - 新增 `"noOpenTabs": "暂无打开的标签页",`
  - 其余（`clearBinding`/`syncHint`/`tabClosed`）不动

`frontend/src/i18n/locales/en/common.json`（同结构）：
- 删除 `"attachedAssets": "Attached assets",`
- `"contextSection": "Linked assets"` → `"Linked tabs"`
- `"contextExpand": "Show linked assets"` → `"Show linked tabs"`
- `"contextCollapse": "Hide linked assets"` → `"Hide linked tabs"`
- `linkedAsset` 对象内：
  - `"pickPlaceholder": "Choose an asset to bind"` → `"Choose a tab to bind"`
  - `"openAssets": "Open assets"` → `"openTabs": "Open tabs"`
  - 删除 `"pickFromLibrary": "Choose from asset library…",`
  - 新增 `"noOpenTabs": "No open tabs",`

- [ ] **Step 5: 跑测试确认通过**

Run（`frontend/`）: `pnpm test src/components/ai/__tests__/LinkedAssetControl.test.tsx src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`
Expected: PASS（两文件全部 it 通过）。

- [ ] **Step 6: 全量前端测试无回归（防 openAssets/attachedAssets 键遗留引用）**

Run（`frontend/`）: `pnpm test src/components/ai src/stores` 或全量 `pnpm test`
Expected: PASS。若有其它文件引用被删/改的键（`openAssets`/`pickFromLibrary`/`attachedAssets`/`menu-terminal-`），在此暴露并修正（应无——已 grep 确认仅这两个测试引用）。

- [ ] **Step 7: 校验两个 JSON 合法 + 改动文件 lint 干净**

Run: `node -e "require('./src/i18n/locales/zh-CN/common.json');require('./src/i18n/locales/en/common.json');console.log('json ok')"`（`frontend/` 下）
Run: `npx eslint src/components/ai/LinkedAssetControl.tsx src/components/ai/__tests__/LinkedAssetControl.test.tsx src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx`
Expected: json ok；eslint 对这些文件无 error。

- [ ] **Step 8: 提交**

```bash
git add frontend/src/components/ai/LinkedAssetControl.tsx \
  frontend/src/components/ai/__tests__/LinkedAssetControl.test.tsx \
  frontend/src/components/ai/__tests__/LinkedAssetControl.binding.test.tsx \
  frontend/src/i18n/locales/zh-CN/common.json \
  frontend/src/i18n/locales/en/common.json
git commit -m "✨ AI 绑定收窄为打开的标签页：删资产库选择 + 列表按 tab + 文案资产→标签页"
```

## Self-Review

- **Spec coverage:** 删库选择入口 ✅(Step3 去 `menu-pick-library`/`AssetSelect`/`picking`)；per-tab 列表 ✅(Step3 `openTabs` 不去重 + Step1 同资产两 tab 测试)；文案全改 ✅(Step4 两语言 + `contextSection` 等)；空态 ✅(`noOpenTabs`)；删死键 `attachedAssets` ✅。
- **Placeholder scan:** 无 TBD/TODO；组件与测试均为完整代码；i18n 逐键给出新旧值。
- **Type consistency:** `bindSidebarTab` 签名与 store 一致（`workspaceTabId: string|null`）；`OpenTab.tabId` = `wt.id`（string）；testid `menu-tab-${tab.id}` 与测试断言一致；键名 `openTabs`/`noOpenTabs` 在组件与两语言一致。
