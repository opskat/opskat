# VNC 面板 UI/UX 重设计 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 VNC 面板采用与 RDP 一致的 chrome（slim 顶栏 + 全面板 overlay + 底部状态栏），并把两者近乎相同的展示原子抽为共享组件（单一事实来源），RDP 一并改用。

**Architecture:** 新增 `frontend/src/components/remote/` 存放 3 个纯展示原子（`RemoteStatusPill` / `RemoteStatusBar` / `RemoteConnectionOverlay`）+ 共享 `remoteChrome.ts`（`RemoteStatus` 类型 + `formatDuration`）。RDP 的 `RDPChrome.tsx` 改为消费这些原子（保留全部 testid 与行为）。VNC 新增 `VNCToolbar.tsx` 并重写 `VNCPanel.tsx` 到新布局，新增特殊键 / 分辨率 / 运行时长 / 剪贴板同步开关 / 指纹校验入 overlay。

**Tech Stack:** React 19 + TypeScript，Vitest + @testing-library/react，Tailwind（OpsKat oklch tokens），`@opskat/ui`（`Button` / `DropdownMenu*` / `cn`）、`@/components/asset/fields` 的 `Segmented`、`lucide-react`、`react-i18next`。

## Global Constraints

- **TDD**：每个可测单元先写失败测试，再实现。（AGENTS.md Fix policy）
- **提交用 gitmoji**；subject 不带 issue/PR 编号（除非刻意关联 issue）。
- **成功 toast 走 notify helper**：`notifySuccess`（top-center），**不要**直接 `toast.success`；错误仍 `toast.error`（右下角）。
- **i18n**：en 与 zh-CN 同步新增/删除同一批 key；各语言用地道表达，不逐字对齐。
- **lint 零警告**：react-x 全 error，未使用的 import/变量会报错——每次改文件后确保 import 集合精确。
- **不改后端**；不动 VNC 会话 / IPC / transport effect 的两阶段启动逻辑（`channel.markOpen()` → `StartVNCStream`）。
- **RDP 回归**：`RDPChrome.tsx` 重构后，`src/components/rdp/__tests__/*` 全部 testid 与断言须仍绿——共享原子必须支持传入 testid。
- **命令**（在 `frontend/` 下）：单测 `pnpm vitest run <path>`；全量 `pnpm vitest run`；lint `pnpm lint`。

---

### Task 1: 共享 `remoteChrome.ts`（`RemoteStatus` + `formatDuration`）

把 `formatDuration` 从 RDP 专属的 `rdpInput.ts` 迁到共享模块，`rdpInput.ts` 改为 re-export（保持 `rdpInput.test.ts` 与 `RDPChrome` 的现有 import 不破）。

**Files:**
- Create: `frontend/src/components/remote/remoteChrome.ts`
- Modify: `frontend/src/components/rdp/rdpInput.ts:265-270`（把 `formatDuration` 定义替换为 re-export）
- Test: `frontend/src/components/remote/__tests__/remoteChrome.test.ts`

**Interfaces:**
- Produces: `type RemoteStatus = "connecting" | "connected" | "error" | "closed"`；`function formatDuration(totalSeconds: number): string`（输出 `HH:MM:SS`，与旧实现逐字一致）。

- [ ] **Step 1: 写失败测试**

`frontend/src/components/remote/__tests__/remoteChrome.test.ts`：
```ts
import { describe, expect, it } from "vitest";
import { formatDuration } from "@/components/remote/remoteChrome";

describe("formatDuration", () => {
  it("formats seconds as HH:MM:SS", () => {
    expect(formatDuration(0)).toBe("00:00:00");
    expect(formatDuration(252)).toBe("00:04:12");
    expect(formatDuration(3661)).toBe("01:01:01");
  });
  it("clamps negatives to zero", () => {
    expect(formatDuration(-5)).toBe("00:00:00");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm vitest run src/components/remote/__tests__/remoteChrome.test.ts`
Expected: FAIL —— 模块不存在（Cannot find module `@/components/remote/remoteChrome`）。

- [ ] **Step 3: 实现共享模块**

`frontend/src/components/remote/remoteChrome.ts`：
```ts
/** 远程桌面会话状态（RDP / VNC 共用）。 */
export type RemoteStatus = "connecting" | "connected" | "error" | "closed";

/** Format elapsed seconds as HH:MM:SS. */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}
```

- [ ] **Step 4: `rdpInput.ts` 改 re-export**

把 `frontend/src/components/rdp/rdpInput.ts` 中 `formatDuration` 的定义（`/** Format elapsed seconds as HH:MM:SS. */` 起的整个函数）替换为：
```ts
export { formatDuration } from "@/components/remote/remoteChrome";
```

- [ ] **Step 5: 跑测试确认通过（含 RDP 的既有 formatDuration 测试）**

Run: `pnpm vitest run src/components/remote/__tests__/remoteChrome.test.ts src/components/rdp/__tests__/rdpInput.test.ts`
Expected: PASS（两个文件都绿，证明 re-export 未破坏 RDP）。

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/remote/remoteChrome.ts frontend/src/components/remote/__tests__/remoteChrome.test.ts frontend/src/components/rdp/rdpInput.ts
git commit -m "♻️ 抽出共享 remoteChrome(RemoteStatus + formatDuration)"
```

---

### Task 2: `RemoteStatusPill`

**Files:**
- Create: `frontend/src/components/remote/RemoteStatusPill.tsx`
- Test: `frontend/src/components/remote/__tests__/RemoteStatusPill.test.tsx`

**Interfaces:**
- Consumes: `RemoteStatus`（Task 1）。
- Produces: `function RemoteStatusPill(props: { status: RemoteStatus; label: string; testid?: string }): JSX.Element`。

- [ ] **Step 1: 写失败测试**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RemoteStatusPill } from "@/components/remote/RemoteStatusPill";

describe("RemoteStatusPill", () => {
  it("renders label and status-specific color classes", () => {
    render(<RemoteStatusPill status="connected" label="Connected" testid="x-status" />);
    const pill = screen.getByTestId("x-status");
    expect(pill).toHaveTextContent("Connected");
    expect(pill.className).toContain("text-success");
  });
  it("maps error status to destructive", () => {
    render(<RemoteStatusPill status="error" label="Error" testid="x-status" />);
    expect(screen.getByTestId("x-status").className).toContain("text-destructive");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm vitest run src/components/remote/__tests__/RemoteStatusPill.test.tsx`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现组件**

`frontend/src/components/remote/RemoteStatusPill.tsx`：
```tsx
import { cn } from "@opskat/ui";
import type { RemoteStatus } from "./remoteChrome";

const STATUS_PILL: Record<RemoteStatus, string> = {
  connecting: "border-warning/25 bg-warning/15 text-warning",
  connected: "border-success/25 bg-success/15 text-success",
  error: "border-destructive/25 bg-destructive/15 text-destructive",
  closed: "border-border bg-muted text-muted-foreground",
};

export function RemoteStatusPill({
  status,
  label,
  testid,
}: {
  status: RemoteStatus;
  label: string;
  testid?: string;
}) {
  return (
    <span
      data-testid={testid}
      className={cn(
        "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 text-[11px] font-medium",
        STATUS_PILL[status]
      )}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {label}
    </span>
  );
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm vitest run src/components/remote/__tests__/RemoteStatusPill.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/remote/RemoteStatusPill.tsx frontend/src/components/remote/__tests__/RemoteStatusPill.test.tsx
git commit -m "✨ 新增共享 RemoteStatusPill"
```

---

### Task 3: `RemoteStatusBar`

**Files:**
- Create: `frontend/src/components/remote/RemoteStatusBar.tsx`
- Test: `frontend/src/components/remote/__tests__/RemoteStatusBar.test.tsx`

**Interfaces:**
- Consumes: `formatDuration`（Task 1）。
- Produces: `function RemoteStatusBar(props: { width: number; height: number; showFit: boolean; fitLabel: string; connected: boolean; elapsed: number; extra?: React.ReactNode }): JSX.Element`。

- [ ] **Step 1: 写失败测试**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";

describe("RemoteStatusBar", () => {
  it("shows resolution, fit badge and uptime when connected", () => {
    render(
      <RemoteStatusBar width={1920} height={1080} showFit fitLabel="Auto-fit" connected elapsed={3661} />
    );
    expect(screen.getByText("1920 × 1080")).toBeInTheDocument();
    expect(screen.getByText("Auto-fit")).toBeInTheDocument();
    expect(screen.getByText("01:01:01")).toBeInTheDocument();
  });
  it("shows a dash when resolution is unknown and hides fit badge/uptime", () => {
    render(<RemoteStatusBar width={0} height={0} showFit={false} fitLabel="Auto-fit" connected={false} elapsed={0} />);
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("Auto-fit")).not.toBeInTheDocument();
    expect(screen.queryByText("00:00:00")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm vitest run src/components/remote/__tests__/RemoteStatusBar.test.tsx`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现组件**

`frontend/src/components/remote/RemoteStatusBar.tsx`：
```tsx
import type { ReactNode } from "react";
import { Scaling } from "lucide-react";
import { formatDuration } from "./remoteChrome";

export function RemoteStatusBar({
  width,
  height,
  showFit,
  fitLabel,
  connected,
  elapsed,
  extra,
}: {
  width: number;
  height: number;
  showFit: boolean;
  fitLabel: string;
  connected: boolean;
  elapsed: number;
  extra?: ReactNode;
}) {
  return (
    <div className="flex h-6 shrink-0 items-center gap-3 border-t bg-muted/30 px-3 text-[11px] text-muted-foreground">
      <span className="flex items-center gap-1.5 font-mono">
        <Scaling className="h-3 w-3" />
        {width > 0 ? `${width} × ${height}` : "—"}
      </span>
      {showFit && (
        <span className="rounded border border-info/25 bg-info/15 px-1.5 py-px text-[11px] text-info">{fitLabel}</span>
      )}
      {extra}
      {connected && <span className="ml-auto font-mono tabular-nums">{formatDuration(elapsed)}</span>}
    </div>
  );
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm vitest run src/components/remote/__tests__/RemoteStatusBar.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/remote/RemoteStatusBar.tsx frontend/src/components/remote/__tests__/RemoteStatusBar.test.tsx
git commit -m "✨ 新增共享 RemoteStatusBar"
```

---

### Task 4: `RemoteConnectionOverlay`

**Files:**
- Create: `frontend/src/components/remote/RemoteConnectionOverlay.tsx`
- Test: `frontend/src/components/remote/__tests__/RemoteConnectionOverlay.test.tsx`

**Interfaces:**
- Consumes: `RemoteStatus`（Task 1）。
- Produces:
```ts
function RemoteConnectionOverlay(props: {
  status: RemoteStatus;
  error: string;
  host: string;
  port: number;
  labels: { connecting: string; error: string; closed: string; reconnect: string; edit?: string };
  onReconnect: () => void;
  onEdit?: () => void;
  reconnectTestId?: string;
  editTestId?: string;
}): JSX.Element | null
```
（`status === "connected"` 时返回 `null`。）

- [ ] **Step 1: 写失败测试**

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";

const labels = { connecting: "Connecting...", error: "Failed", closed: "Disconnected", reconnect: "Reconnect", edit: "Edit" };

describe("RemoteConnectionOverlay", () => {
  it("returns null when connected", () => {
    const { container } = render(
      <RemoteConnectionOverlay status="connected" error="" host="h" port={1} labels={labels} onReconnect={() => {}} />
    );
    expect(container.firstChild).toBeNull();
  });
  it("shows host:port while connecting", () => {
    render(<RemoteConnectionOverlay status="connecting" error="" host="10.0.0.1" port={5901} labels={labels} onReconnect={() => {}} />);
    expect(screen.getByText("Connecting...")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.1:5901")).toBeInTheDocument();
  });
  it("shows error + reconnect/edit with testids and fires callbacks", () => {
    const onReconnect = vi.fn();
    const onEdit = vi.fn();
    render(
      <RemoteConnectionOverlay
        status="error" error="boom" host="h" port={1} labels={labels}
        onReconnect={onReconnect} onEdit={onEdit} reconnectTestId="x-reconnect" editTestId="x-edit"
      />
    );
    expect(screen.getByText("boom")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("x-reconnect"));
    fireEvent.click(screen.getByTestId("x-edit"));
    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm vitest run src/components/remote/__tests__/RemoteConnectionOverlay.test.tsx`
Expected: FAIL —— 组件不存在。

- [ ] **Step 3: 实现组件**

`frontend/src/components/remote/RemoteConnectionOverlay.tsx`：
```tsx
import { AlertTriangle, Loader2, Power, RefreshCw, Settings2 } from "lucide-react";
import { Button } from "@opskat/ui";
import type { RemoteStatus } from "./remoteChrome";

export function RemoteConnectionOverlay({
  status,
  error,
  host,
  port,
  labels,
  onReconnect,
  onEdit,
  reconnectTestId,
  editTestId,
}: {
  status: RemoteStatus;
  error: string;
  host: string;
  port: number;
  labels: { connecting: string; error: string; closed: string; reconnect: string; edit?: string };
  onReconnect: () => void;
  onEdit?: () => void;
  reconnectTestId?: string;
  editTestId?: string;
}) {
  if (status === "connected") return null;

  if (status === "connecting") {
    return (
      <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background text-sm text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin text-primary" />
        <div className="font-medium text-foreground">{labels.connecting}</div>
        {host && (
          <div className="font-mono text-xs">
            {host}:{port}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background px-6 text-center">
      {status === "error" ? (
        <AlertTriangle className="h-8 w-8 text-destructive" />
      ) : (
        <Power className="h-8 w-8 text-muted-foreground" />
      )}
      <div className="text-base font-semibold text-foreground">{status === "error" ? labels.error : labels.closed}</div>
      {status === "error" && error && <div className="max-w-xl break-words text-sm text-muted-foreground">{error}</div>}
      <div className="mt-1 flex items-center gap-2.5">
        <Button type="button" size="sm" className="gap-1.5" data-testid={reconnectTestId} onClick={onReconnect}>
          <RefreshCw className="h-3.5 w-3.5" />
          {labels.reconnect}
        </Button>
        {onEdit && labels.edit && (
          <Button type="button" variant="outline" size="sm" className="gap-1.5" data-testid={editTestId} onClick={onEdit}>
            <Settings2 className="h-3.5 w-3.5" />
            {labels.edit}
          </Button>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm vitest run src/components/remote/__tests__/RemoteConnectionOverlay.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/remote/RemoteConnectionOverlay.tsx frontend/src/components/remote/__tests__/RemoteConnectionOverlay.test.tsx
git commit -m "✨ 新增共享 RemoteConnectionOverlay"
```

---

### Task 5: 重构 RDP 消费共享原子（行为 / testid 不变）

**Files:**
- Modify: `frontend/src/components/rdp/RDPChrome.tsx`
- 验证：`frontend/src/components/rdp/__tests__/RDPPanel.test.tsx`（不改，必须仍绿）

**Interfaces:**
- Consumes: `RemoteStatusPill` / `RemoteStatusBar` / `RemoteConnectionOverlay`（Task 2–4）。

- [ ] **Step 1: 先跑 RDP 测试，记录基线全绿**

Run: `pnpm vitest run src/components/rdp/__tests__/RDPPanel.test.tsx`
Expected: PASS（改前基线）。

- [ ] **Step 2: 改 `RDPChrome.tsx` import 段**

把顶部 import 段替换为（移除迁往共享原子后不再直接使用的图标，新增共享原子；保留 `formatDuration` 已不在此文件使用则删除其 import）：
```tsx
import { Clipboard, ClipboardCheck, Keyboard, Maximize2, Minimize2, Monitor, Power } from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/asset/fields";
import { SCANCODE } from "./rdpInput";
import { RemoteStatusPill } from "@/components/remote/RemoteStatusPill";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";
```
并删除文件内已废弃的本地 `const STATUS_PILL: Record<RDPStatus, string> = { ... };` 定义。`RDPStatus` 类型定义保留（`RDPPanel` 仍在用），或改为 `export type RDPStatus = RemoteStatus;`（二选一，保持 `RDPToolbar`/`RDPPanel` 的 `RDPStatus` import 不变）。

- [ ] **Step 3: `RDPToolbar` 内状态胶囊改用共享原子**

把 `RDPToolbar` 里这段：
```tsx
      <span
        data-testid="rdp-status"
        className={cn(
          "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 font-medium",
          STATUS_PILL[status]
        )}
      >
        <span className="h-1.5 w-1.5 rounded-full bg-current" />
        {status === "connected" ? t("asset.rdpConnected") : t(`asset.rdpStatus.${status}`)}
      </span>
```
替换为：
```tsx
      <RemoteStatusPill
        status={status}
        label={status === "connected" ? t("asset.rdpConnected") : t(`asset.rdpStatus.${status}`)}
        testid="rdp-status"
      />
```

- [ ] **Step 4: `RDPSessionOverlay` 改为薄封装**

把整个 `export function RDPSessionOverlay({...}) { ... }` 函数体替换为：
```tsx
export function RDPSessionOverlay({
  status,
  error,
  host,
  port,
  onReconnect,
  onEdit,
}: {
  status: RDPStatus;
  error: string;
  host: string;
  port: number;
  onReconnect: () => void;
  onEdit?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <RemoteConnectionOverlay
      status={status}
      error={error}
      host={host}
      port={port}
      labels={{
        connecting: t("asset.rdpConnecting"),
        error: t("asset.rdpError"),
        closed: t("asset.rdpDisconnected"),
        reconnect: t("asset.rdpReconnect"),
        edit: t("asset.rdpEditConnection"),
      }}
      onReconnect={onReconnect}
      onEdit={onEdit}
      reconnectTestId="rdp-reconnect"
      editTestId="rdp-edit"
    />
  );
}
```

- [ ] **Step 5: `RDPStatusBar` 改为薄封装**

把整个 `export function RDPStatusBar({...}) { ... }` 函数体替换为：
```tsx
export function RDPStatusBar({
  width,
  height,
  viewMode,
  connected,
  elapsed,
}: {
  width: number;
  height: number;
  viewMode: RDPViewMode;
  connected: boolean;
  elapsed: number;
}) {
  const { t } = useTranslation();
  return (
    <RemoteStatusBar
      width={width}
      height={height}
      showFit={viewMode === "fit"}
      fitLabel={t("asset.rdpAutoFit")}
      connected={connected}
      elapsed={elapsed}
    />
  );
}
```

- [ ] **Step 6: 跑 RDP 测试 + lint 确认无回归**

Run: `pnpm vitest run src/components/rdp/__tests__/ && pnpm lint`
Expected: PASS（testid `rdp-status`/`rdp-reconnect`/`rdp-edit`、host:port chip、view/clipboard/fullscreen/disconnect 断言全绿；lint 无未使用 import 报错——`Scaling`/`AlertTriangle`/`Loader2`/`RefreshCw`/`Settings2`/`formatDuration` 已从本文件移除）。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/rdp/RDPChrome.tsx
git commit -m "♻️ RDP chrome 改用共享 Remote 展示原子"
```

---

### Task 6: `VNCToolbar`（+ 新增 VNC i18n key）

**Files:**
- Create: `frontend/src/components/vnc/VNCToolbar.tsx`
- Modify: `frontend/src/i18n/locales/en/common.json`（`vnc` 段）、`frontend/src/i18n/locales/zh-CN/common.json`（`vnc` 段）
- Test: `frontend/src/components/vnc/__tests__/VNCToolbar.test.tsx`

**Interfaces:**
- Consumes: `RemoteStatusPill`（Task 2）、`RemoteStatus`（Task 1）、`Segmented`。
- Produces:
```ts
type VNCViewMode = "fit" | "original";
type VNCSpecialKey = "ctrl-alt-del" | "alt-tab" | "esc";
function VNCToolbar(props: {
  assetName: string; host: string; port: number;
  status: RemoteStatus; statusLabel: string;
  viewMode: VNCViewMode; clipboardEnabled: boolean;
  filesEnabled: boolean; filesOpen: boolean; isFullscreen: boolean;
  onViewModeChange: (m: VNCViewMode) => void;
  onSendSpecialKey: (k: VNCSpecialKey) => void;
  onToggleClipboard: () => void; onToggleFiles: () => void;
  onToggleFullscreen: () => void; onDisconnect: () => void;
}): JSX.Element
```

- [ ] **Step 1: 新增 i18n key（en）**

在 `frontend/src/i18n/locales/en/common.json` 的 `"vnc": { ... }` 对象内：**新增**以下键（放在 `files` 附近），并**删除** `scaleOn` / `scaleOff` / `pasteText` / `fileChannelDisabled` 三个已废弃键：
```json
"viewMode": "VNC view mode",
"viewFit": "Fit",
"viewOriginal": "1:1",
"specialKeys": "Special keys",
"disconnect": "Disconnect",
"exitFullscreen": "Exit fullscreen",
"fileChannelUnavailable": "No SSH/SFTP file channel configured",
"clipboardOn": "Clipboard sync: on",
"clipboardOff": "Clipboard sync: off",
"clipboardSynced": "Clipboard synced",
"clipboardEnabled": "VNC clipboard sync enabled",
"clipboardDisabled": "VNC clipboard sync disabled",
"editConnection": "Edit connection",
"autoFit": "Auto-fit to window",
"errorTitle": "VNC connection failed",
"disconnectedTitle": "Disconnected",
"status": { "connecting": "Connecting", "connected": "Connected", "error": "Error", "closed": "Closed" }
```

- [ ] **Step 2: 新增 i18n key（zh-CN，地道表达）**

在 `frontend/src/i18n/locales/zh-CN/common.json` 的 `"vnc": { ... }` 内新增同名键、并删除同样三个废弃键：
```json
"viewMode": "VNC 显示模式",
"viewFit": "适应",
"viewOriginal": "原始",
"specialKeys": "特殊按键",
"disconnect": "断开",
"exitFullscreen": "退出全屏",
"fileChannelUnavailable": "未配置 SSH/SFTP 文件通道",
"clipboardOn": "剪贴板同步：开",
"clipboardOff": "剪贴板同步：关",
"clipboardSynced": "剪贴板已同步",
"clipboardEnabled": "已开启 VNC 剪贴板同步",
"clipboardDisabled": "已关闭 VNC 剪贴板同步",
"editConnection": "编辑连接",
"autoFit": "自动适应窗口",
"errorTitle": "VNC 连接失败",
"disconnectedTitle": "已断开",
"status": { "connecting": "连接中", "connected": "已连接", "error": "连接失败", "closed": "已断开" }
```

- [ ] **Step 3: 写失败测试**

`frontend/src/components/vnc/__tests__/VNCToolbar.test.tsx`（`t` 回显 key）：
```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { VNCToolbar } from "@/components/vnc/VNCToolbar";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (k: string) => k, i18n: { language: "en" } }),
}));

function setup(overrides: Partial<React.ComponentProps<typeof VNCToolbar>> = {}) {
  const props = {
    assetName: "desk-01", host: "10.0.0.1", port: 5901,
    status: "connected" as const, statusLabel: "Connected",
    viewMode: "fit" as const, clipboardEnabled: true,
    filesEnabled: true, filesOpen: false, isFullscreen: false,
    onViewModeChange: vi.fn(), onSendSpecialKey: vi.fn(),
    onToggleClipboard: vi.fn(), onToggleFiles: vi.fn(),
    onToggleFullscreen: vi.fn(), onDisconnect: vi.fn(),
    ...overrides,
  };
  render(<VNCToolbar {...props} />);
  return props;
}

describe("VNCToolbar", () => {
  it("renders identity, host chip and status pill", () => {
    setup();
    expect(screen.getByText("desk-01")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.1:5901")).toBeInTheDocument();
    expect(screen.getByTestId("vnc-status")).toHaveTextContent("Connected");
  });
  it("fires view-mode change", () => {
    const p = setup();
    fireEvent.click(screen.getByTestId("vnc-view-original"));
    expect(p.onViewModeChange).toHaveBeenCalledWith("original");
  });
  it("sends Ctrl+Alt+Del from the special-keys menu", () => {
    const p = setup();
    fireEvent.click(screen.getByTestId("vnc-special-keys"));
    fireEvent.click(screen.getByTestId("vnc-key-cad"));
    expect(p.onSendSpecialKey).toHaveBeenCalledWith("ctrl-alt-del");
  });
  it("disables special keys / clipboard / disconnect when not connected", () => {
    setup({ status: "closed" });
    expect(screen.getByTestId("vnc-special-keys")).toBeDisabled();
    expect(screen.getByTestId("vnc-clipboard")).toBeDisabled();
    expect(screen.getByTestId("vnc-disconnect")).toBeDisabled();
  });
  it("disables the files button when there is no channel", () => {
    setup({ filesEnabled: false });
    expect(screen.getByTestId("vnc-files")).toBeDisabled();
  });
  it("reflects clipboard state via data-state", () => {
    setup({ clipboardEnabled: false });
    expect(screen.getByTestId("vnc-clipboard")).toHaveAttribute("data-state", "off");
  });
});
```

- [ ] **Step 4: 跑测试确认失败**

Run: `pnpm vitest run src/components/vnc/__tests__/VNCToolbar.test.tsx`
Expected: FAIL —— 组件不存在。

- [ ] **Step 5: 实现 `VNCToolbar.tsx`**

```tsx
import { Clipboard, ClipboardCheck, FolderOpen, Keyboard, Maximize2, Minimize2, Power, ScreenShare } from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/asset/fields";
import { RemoteStatusPill } from "@/components/remote/RemoteStatusPill";
import type { RemoteStatus } from "@/components/remote/remoteChrome";

export type VNCViewMode = "fit" | "original";
export type VNCSpecialKey = "ctrl-alt-del" | "alt-tab" | "esc";

const SPECIAL_KEYS: { id: VNCSpecialKey; testid: string; label: string }[] = [
  { id: "ctrl-alt-del", testid: "vnc-key-cad", label: "Ctrl + Alt + Del" },
  { id: "alt-tab", testid: "vnc-key-alt-tab", label: "Alt + Tab" },
  { id: "esc", testid: "vnc-key-esc", label: "Esc" },
];

export function VNCToolbar({
  assetName,
  host,
  port,
  status,
  statusLabel,
  viewMode,
  clipboardEnabled,
  filesEnabled,
  filesOpen,
  isFullscreen,
  onViewModeChange,
  onSendSpecialKey,
  onToggleClipboard,
  onToggleFiles,
  onToggleFullscreen,
  onDisconnect,
}: {
  assetName: string;
  host: string;
  port: number;
  status: RemoteStatus;
  statusLabel: string;
  viewMode: VNCViewMode;
  clipboardEnabled: boolean;
  filesEnabled: boolean;
  filesOpen: boolean;
  isFullscreen: boolean;
  onViewModeChange: (m: VNCViewMode) => void;
  onSendSpecialKey: (k: VNCSpecialKey) => void;
  onToggleClipboard: () => void;
  onToggleFiles: () => void;
  onToggleFullscreen: () => void;
  onDisconnect: () => void;
}) {
  const { t } = useTranslation();
  const connected = status === "connected";

  return (
    <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/30 px-3 text-xs">
      <ScreenShare className="h-4 w-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 truncate font-medium text-foreground">{assetName}</span>
      {host && (
        <span className="shrink-0 whitespace-nowrap rounded border bg-background/50 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          {host}:{port}
        </span>
      )}
      <RemoteStatusPill status={status} label={statusLabel} testid="vnc-status" />

      <div className="ml-auto flex shrink-0 items-center gap-2">
        <Segmented<VNCViewMode>
          value={viewMode}
          onChange={onViewModeChange}
          aria-label={t("vnc.viewMode")}
          className="h-7 w-[116px] rounded-md p-0.5"
          options={[
            { value: "fit", label: t("vnc.viewFit"), testid: "vnc-view-fit" },
            { value: "original", label: t("vnc.viewOriginal"), testid: "vnc-view-original" },
          ]}
        />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-7 gap-1.5 px-2"
              data-testid="vnc-special-keys"
              title={t("vnc.specialKeys")}
              disabled={!connected}
            >
              <Keyboard className="h-3.5 w-3.5" />
              Ctrl+Alt+Del
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {SPECIAL_KEYS.map((key) => (
              <DropdownMenuItem key={key.id} data-testid={key.testid} onSelect={() => onSendSpecialKey(key.id)}>
                {key.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <Button
          type="button"
          variant="outline"
          size="icon"
          data-testid="vnc-clipboard"
          data-state={clipboardEnabled ? "on" : "off"}
          title={clipboardEnabled ? t("vnc.clipboardOn") : t("vnc.clipboardOff")}
          className={cn("h-7 w-7", clipboardEnabled ? "text-success" : "text-muted-foreground/60")}
          disabled={!connected}
          onClick={onToggleClipboard}
        >
          {clipboardEnabled ? <ClipboardCheck className="h-3.5 w-3.5" /> : <Clipboard className="h-3.5 w-3.5" />}
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          data-testid="vnc-files"
          data-state={filesOpen ? "on" : "off"}
          title={filesEnabled ? t("vnc.files") : t("vnc.fileChannelUnavailable")}
          className={cn("h-7 w-7", filesOpen && "text-primary")}
          disabled={!filesEnabled}
          onClick={onToggleFiles}
        >
          <FolderOpen className="h-3.5 w-3.5" />
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          className="h-7 w-7"
          data-testid="vnc-fullscreen"
          title={isFullscreen ? t("vnc.exitFullscreen") : t("vnc.fullscreen")}
          onClick={onToggleFullscreen}
        >
          {isFullscreen ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
        </Button>

        <Button
          type="button"
          variant="outline"
          size="icon"
          className="h-7 w-7 text-destructive hover:text-destructive"
          data-testid="vnc-disconnect"
          title={t("vnc.disconnect")}
          disabled={!connected}
          onClick={onDisconnect}
        >
          <Power className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `pnpm vitest run src/components/vnc/__tests__/VNCToolbar.test.tsx`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/vnc/VNCToolbar.tsx frontend/src/components/vnc/__tests__/VNCToolbar.test.tsx frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git commit -m "✨ 新增 VNCToolbar + VNC i18n key"
```

---

### Task 7: 重写 `VNCPanel` 到新 chrome（含特殊键 / 分辨率 / 运行时长 / 剪贴板开关 / 指纹入 overlay）+ MainPanel 接 onEdit

这是核心交付：把面板重排为 `[VNCToolbar] + [viewport(overlay/verify/canvas) + fileDock] + [RemoteStatusBar]`，接入所有新能力，并把指纹校验从 `ConfirmDialog` 改为 overlay。transport effect 的两阶段启动逻辑**保持不变**。

**Files:**
- Modify: `frontend/src/types/novnc.d.ts`（补 `sendCtrlAltDel`）
- Rewrite: `frontend/src/components/vnc/VNCPanel.tsx`
- Modify: `frontend/src/components/layout/MainPanel.tsx:306`（给 `VNCPanel` 传 `onEdit`）
- Modify: `frontend/src/__tests__/VNCPanel.test.tsx`（适配新 UI）

**Interfaces:**
- Consumes: `VNCToolbar` / `VNCViewMode` / `VNCSpecialKey`（Task 6）、`RemoteConnectionOverlay` / `RemoteStatusBar`（Task 3–4）、`RemoteStatus`（Task 1）。
- Produces: `function VNCPanel(props: { tabId: string; asset: asset_entity.Asset; onEdit?: () => void }): JSX.Element`。

- [ ] **Step 1: 给 noVNC 类型补 `sendCtrlAltDel`**

在 `frontend/src/types/novnc.d.ts` 的 `class RFB` 内，`sendKey` 声明旁新增一行：
```ts
    sendCtrlAltDel(): void;
```

- [ ] **Step 2: 写失败测试（更新 `VNCPanel.test.tsx`）**

对 `frontend/src/__tests__/VNCPanel.test.tsx` 做以下改动：

(a) `FakeRFB` 增补方法与 resize 能力：
```ts
  sendCtrlAltDel = vi.fn();
```
（加在 `sendKey = vi.fn();` 下一行。）

(b) 指纹校验测试：把
```ts
    expect(await screen.findByText("vnc.verifyServerTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("confirm-vnc-server"));
```
改为
```ts
    expect(await screen.findByText("vnc.verifyServerTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("vnc-verify-approve"));
```

(c) 文件面板测试：把 `fireEvent.click(screen.getByText("vnc.files"));` 改为 `fireEvent.click(screen.getByTestId("vnc-files"));`。

(d) 两个「点 `vnc.pasteText` 触发粘贴」测试改走 Ctrl+V（粘贴按钮已移除，路径不变）：
- 「sends mixed Chinese and English through one GBK clipboard message」：把
  ```ts
  fireEvent.click(screen.getByText("vnc.pasteText"));
  ```
  改为
  ```ts
  const vncContainer = document.querySelector('[tabindex="0"]')!;
  fireEvent.keyDown(vncContainer, { key: "v", code: "KeyV", ctrlKey: true });
  ```
- 「does not forward Ctrl+V when the text was typed directly via keysym fallback」：同样把 `fireEvent.click(screen.getByText("vnc.pasteText"));` 改为上面的 `keyDown` 写法。

(e) 新增两条测试（放在 describe 末尾）：
```ts
  it("sends Ctrl+Alt+Del through noVNC's built-in helper", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);
    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!._rfbConnectionState = "connected";
    FakeRFB.latest!.dispatchEvent(new CustomEvent("connect"));
    fireEvent.click(await screen.findByTestId("vnc-special-keys"));
    fireEvent.click(await screen.findByTestId("vnc-key-cad"));
    expect(FakeRFB.latest!.sendCtrlAltDel).toHaveBeenCalledTimes(1);
  });

  it("stops mirroring the remote clipboard when sync is turned off", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);
    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!._rfbConnectionState = "connected";
    FakeRFB.latest!.dispatchEvent(new CustomEvent("connect"));
    fireEvent.click(await screen.findByTestId("vnc-clipboard")); // turn off
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: "hello" } }));
    await new Promise((r) => setTimeout(r, 20));
    expect(ClipboardSetText).not.toHaveBeenCalled();
  });
```

- [ ] **Step 3: 跑测试确认失败**

Run: `pnpm vitest run src/__tests__/VNCPanel.test.tsx`
Expected: FAIL —— 新 testid（`vnc-verify-approve` / `vnc-files` / `vnc-special-keys` / `vnc-clipboard`）不存在、`sendCtrlAltDel` 未被调用。

- [ ] **Step 4: 重写 `VNCPanel.tsx`**

用以下完整文件替换 `frontend/src/components/vnc/VNCPanel.tsx`：
```tsx
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ClipboardEvent as ReactClipboardEvent,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { Fingerprint } from "lucide-react";
import { Button } from "@opskat/ui";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { asset_entity } from "../../../wailsjs/go/models";
import { ConnectVNC, DisconnectVNC, EncodeVNCClipboardText, StartVNCStream } from "../../../wailsjs/go/vnc/VNC";
import { DisconnectSSH, OpenSFTPSession } from "../../../wailsjs/go/ssh/SSH";
import { ClipboardGetText, ClipboardSetText } from "../../../wailsjs/runtime";
import { FileManagerPanel } from "@/components/terminal/FileManagerPanel";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";
import { decodeVNCClipboardText, pasteVNCClipboardText } from "@/lib/vncClipboard";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";
import type { RemoteStatus } from "@/components/remote/remoteChrome";
import { VNCToolbar, type VNCSpecialKey, type VNCViewMode } from "./VNCToolbar";
import type RFB from "@novnc/novnc/lib/rfb";

interface VNCPanelProps {
  tabId: string;
  asset: asset_entity.Asset;
  onEdit?: () => void;
}

interface VNCSession {
  id: string;
  username?: string;
  password?: string;
  fileSshAssetId: number;
}

interface VNCConnectionConfig {
  host?: string;
  port?: number;
}

function parseVNCEndpoint(configJSON: string): { host: string; port: number } {
  try {
    const cfg: VNCConnectionConfig = JSON.parse(configJSON || "{}");
    return { host: cfg.host || "", port: cfg.port || 5900 };
  } catch {
    return { host: "", port: 5900 };
  }
}

export function VNCPanel({ tabId, asset, onEdit }: VNCPanelProps) {
  const { t } = useTranslation();
  const { host, port } = parseVNCEndpoint(asset.Config);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const vncContainerRef = useRef<HTMLDivElement | null>(null);
  const rfbRef = useRef<RFB | null>(null);
  const errorRef = useRef("");
  const scaleViewportRef = useRef(true);
  const keyboardPasteRef = useRef(false);
  const clipboardEnabledRef = useRef(true);
  const tRef = useRef(t);
  const [session, setSession] = useState<VNCSession | null>(null);
  const [status, setStatus] = useState<RemoteStatus>("connecting");
  const [error, setError] = useState("");
  const [viewMode, setViewMode] = useState<VNCViewMode>("fit");
  const [clipboardEnabled, setClipboardEnabled] = useState(true);
  const [fileOpen, setFileOpen] = useState(false);
  const [fileWidth, setFileWidth] = useState(320);
  const [fileSessionId, setFileSessionId] = useState("");
  const [serverFingerprint, setServerFingerprint] = useState("");
  const [connectedAt, setConnectedAt] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [resolution, setResolution] = useState<{ width: number; height: number }>({ width: 0, height: 0 });
  const [isFullscreen, setIsFullscreen] = useState(false);

  const connect = useCallback(async () => {
    setStatus("connecting");
    setError("");
    errorRef.current = "";
    setServerFingerprint("");
    setConnectedAt(null);
    setResolution({ width: 0, height: 0 });
    if (rfbRef.current) {
      try {
        rfbRef.current.disconnect();
      } catch {
        // ignore stale noVNC instance cleanup
      }
      rfbRef.current = null;
    }
    try {
      const next = (await ConnectVNC(asset.ID)) as VNCSession;
      setSession(next);
      setStatus("connecting");
    } catch (e) {
      const message = String(e);
      errorRef.current = message;
      setError(message);
      setStatus("error");
    }
  }, [asset.ID]);

  useEffect(() => {
    const timer = window.setTimeout(() => void connect(), 0);
    return () => window.clearTimeout(timer);
  }, [connect]);

  useEffect(() => {
    tRef.current = t;
  }, [t]);

  useEffect(() => {
    if (!session || !vncContainerRef.current) return;
    let disposed = false;
    let connectionStatePoll: number | undefined;
    const container = vncContainerRef.current;
    container.innerHTML = "";
    setStatus("connecting");
    const channel = new WailsRfbChannel(session.id);
    const readResolution = () => {
      const canvas = container.querySelector("canvas");
      if (canvas && canvas.width > 0) setResolution({ width: canvas.width, height: canvas.height });
    };
    const markVNCConnected = () => {
      if (disposed) return;
      errorRef.current = "";
      setError("");
      setStatus("connected");
      setConnectedAt((prev) => prev ?? Date.now());
      readResolution();
      window.requestAnimationFrame(() => {
        if (!disposed) readResolution();
      });
    };
    import("@novnc/novnc/lib/rfb")
      .then(({ default: RFBClient }) => {
        if (disposed || !container) {
          channel.close();
          return;
        }
        const rfb = new RFBClient(container, channel, {
          credentials: { username: session.username || "", password: session.password || "" },
        });
        rfb.scaleViewport = scaleViewportRef.current;
        rfb.clipViewport = true;
        rfb.resizeSession = false;
        rfb.background = "#000";
        rfb.addEventListener("connect", markVNCConnected);
        rfb.addEventListener("desktopname", markVNCConnected);
        rfb.addEventListener("capabilities", markVNCConnected);
        rfb.addEventListener("disconnect", (event) => {
          const e = event as CustomEvent<{ clean?: boolean }>;
          if (e.detail?.clean) {
            if (!disposed) setStatus("closed");
            return;
          }
          const message = errorRef.current || tRef.current("vnc.disconnected");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("securityfailure", (event) => {
          const e = event as CustomEvent<{ status?: number; reason?: string }>;
          if (e.detail?.reason) {
            console.warn("VNC security failure", { status: e.detail?.status, reason: e.detail.reason });
          }
          const message = tRef.current("vnc.securityFailed");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("credentialsrequired", () => {
          const message = tRef.current("vnc.credentialsRequired");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("serververification", (event) => {
          const e = event as CustomEvent<{ publickey?: Uint8Array }>;
          const publicKey = e.detail?.publickey;
          if (!publicKey) {
            const message = tRef.current("vnc.serverVerificationFailed");
            errorRef.current = message;
            setError(message);
            setStatus("error");
            return;
          }
          void window.crypto.subtle.digest("SHA-256", new Uint8Array(publicKey)).then((digest) => {
            if (disposed) return;
            const fingerprint = Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0"))
              .join(":")
              .toUpperCase();
            setServerFingerprint(fingerprint);
          });
        });
        rfb.addEventListener("clipboard", (event) => {
          if (!clipboardEnabledRef.current) return;
          const e = event as CustomEvent<{ text?: string }>;
          ClipboardSetText(decodeVNCClipboardText(e.detail?.text || "")).catch((error) => toast.error(String(error)));
        });
        rfbRef.current = rfb;
        // 两阶段:先 markOpen(触发 onopen → noVNC 就绪),再启动后端读 pump,
        // 保证前端已订阅事件、noVNC 已就绪之后字节才开始流动,不丢 RFB 握手首包。
        channel.markOpen();
        void StartVNCStream(session.id).catch((e) => {
          if (disposed) return;
          const message = String(e);
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        connectionStatePoll = window.setInterval(() => {
          if (!disposed && rfb._rfbConnectionState === "connected") {
            markVNCConnected();
            if (connectionStatePoll) window.clearInterval(connectionStatePoll);
          }
        }, 250);
        window.setTimeout(() => {
          if (connectionStatePoll) window.clearInterval(connectionStatePoll);
        }, 15000);
      })
      .catch((e) => {
        const message = String(e);
        errorRef.current = message;
        setError(message);
        setStatus("error");
      });
    return () => {
      disposed = true;
      channel.close();
      if (rfbRef.current) {
        try {
          rfbRef.current.disconnect();
        } catch {
          // ignore stale noVNC instance cleanup
        }
        rfbRef.current = null;
      }
      if (connectionStatePoll) window.clearInterval(connectionStatePoll);
      container.innerHTML = "";
    };
  }, [session]);

  useEffect(() => {
    const fit = viewMode === "fit";
    scaleViewportRef.current = fit;
    if (rfbRef.current) rfbRef.current.scaleViewport = fit;
  }, [viewMode]);

  useEffect(() => {
    if (status !== "connected" || connectedAt === null) return;
    const tick = () => setElapsed(Math.floor((Date.now() - connectedAt) / 1000));
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [status, connectedAt]);

  useEffect(() => {
    const onChange = () => setIsFullscreen(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", onChange);
    return () => document.removeEventListener("fullscreenchange", onChange);
  }, []);

  useEffect(() => {
    return () => {
      if (session?.id) DisconnectVNC(session.id);
    };
  }, [session?.id]);

  useEffect(() => {
    return () => {
      if (fileSessionId) DisconnectSSH(fileSessionId);
    };
  }, [fileSessionId]);

  const openFiles = async () => {
    if (!session?.fileSshAssetId) return;
    setFileOpen((v) => !v);
    if (!fileSessionId) {
      try {
        setFileSessionId(await OpenSFTPSession(session.fileSshAssetId));
      } catch (e) {
        toast.error(String(e));
      }
    }
  };

  const pasteTextToVNC = async (text: string) => {
    const rfb = rfbRef.current;
    if (!rfb || !text || !clipboardEnabledRef.current) return;
    const clipboardSet = await pasteVNCClipboardText(rfb, text, EncodeVNCClipboardText);
    // When the text couldn't be placed on the remote clipboard it was typed
    // directly via keysyms; a follow-up Ctrl+V would paste stale clipboard data.
    if (rfbRef.current !== rfb || !clipboardSet) return;
    rfb.sendKey(0xffe3, "ControlLeft", true);
    rfb.sendKey(0x76, "KeyV", true);
    rfb.sendKey(0x76, "KeyV", false);
    rfb.sendKey(0xffe3, "ControlLeft", false);
  };

  const pasteToVNC = async () => {
    try {
      const text = await ClipboardGetText();
      await pasteTextToVNC(text);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleVNCPaste = (event: ReactClipboardEvent<HTMLDivElement>) => {
    if (!rfbRef.current || !clipboardEnabledRef.current) return;
    if (keyboardPasteRef.current) {
      event.preventDefault();
      return;
    }
    const text = event.clipboardData.getData("text/plain");
    if (!text) return;
    event.preventDefault();
    void pasteTextToVNC(text);
  };

  const handleVNCKeyDownCapture = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!(event.ctrlKey || event.metaKey) || event.altKey || event.key.toLowerCase() !== "v") return;
    if (!clipboardEnabledRef.current) return;
    event.preventDefault();
    event.stopPropagation();
    keyboardPasteRef.current = true;
    void pasteToVNC().finally(() => {
      keyboardPasteRef.current = false;
    });
  };

  const sendSpecialKey = (key: VNCSpecialKey) => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    if (key === "ctrl-alt-del") {
      rfb.sendCtrlAltDel();
      return;
    }
    if (key === "alt-tab") {
      rfb.sendKey(0xffe9, "AltLeft", true);
      rfb.sendKey(0xff09, "Tab", true);
      rfb.sendKey(0xff09, "Tab", false);
      rfb.sendKey(0xffe9, "AltLeft", false);
      return;
    }
    rfb.sendKey(0xff1b, "Escape", true);
    rfb.sendKey(0xff1b, "Escape", false);
  };

  const toggleClipboard = () => {
    const next = !clipboardEnabledRef.current;
    clipboardEnabledRef.current = next;
    setClipboardEnabled(next);
    notifySuccess(t(next ? "vnc.clipboardEnabled" : "vnc.clipboardDisabled"));
  };

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen();
    } else {
      void panelRef.current?.requestFullscreen();
    }
  };

  const disconnectSession = () => {
    const rfb = rfbRef.current;
    if (rfb) {
      try {
        rfb.disconnect();
      } catch {
        // ignore stale noVNC instance cleanup
      }
    }
    if (session?.id) DisconnectVNC(session.id);
    setStatus("closed");
    setConnectedAt(null);
  };

  const approveVNCServer = () => {
    setServerFingerprint("");
    rfbRef.current?.approveServer();
  };

  const cancelVNCServerVerification = () => {
    setServerFingerprint("");
    const message = t("vnc.serverVerificationCancelled");
    errorRef.current = message;
    setError(message);
    setStatus("error");
    rfbRef.current?.disconnect();
  };

  const connected = status === "connected";
  const filesEnabled = !!session?.fileSshAssetId;

  return (
    <div ref={panelRef} className="flex h-full min-h-0 flex-col bg-background">
      <VNCToolbar
        assetName={asset.Name}
        host={host}
        port={port}
        status={status}
        statusLabel={t(`vnc.status.${status}`)}
        viewMode={viewMode}
        clipboardEnabled={clipboardEnabled}
        filesEnabled={filesEnabled}
        filesOpen={fileOpen && !!fileSessionId}
        isFullscreen={isFullscreen}
        onViewModeChange={setViewMode}
        onSendSpecialKey={sendSpecialKey}
        onToggleClipboard={toggleClipboard}
        onToggleFiles={() => void openFiles()}
        onToggleFullscreen={toggleFullscreen}
        onDisconnect={disconnectSession}
      />

      <div className="flex min-h-0 flex-1">
        <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden bg-black">
          <RemoteConnectionOverlay
            status={serverFingerprint ? "connecting" : status}
            error={error}
            host={host}
            port={port}
            labels={{
              connecting: t("vnc.connecting"),
              error: t("vnc.errorTitle"),
              closed: t("vnc.disconnectedTitle"),
              reconnect: t("vnc.reconnect"),
              edit: onEdit ? t("vnc.editConnection") : undefined,
            }}
            onReconnect={() => void connect()}
            onEdit={onEdit}
            reconnectTestId="vnc-reconnect"
            editTestId="vnc-edit"
          />

          {serverFingerprint && (
            <div className="absolute inset-0 z-30 flex flex-col items-center justify-center gap-3 bg-background px-6 text-center">
              <Fingerprint className="h-8 w-8 text-primary" />
              <div className="text-base font-semibold text-foreground">{t("vnc.verifyServerTitle")}</div>
              <div className="max-w-md text-sm text-muted-foreground">{t("vnc.verifyServerDesc")}</div>
              <div className="w-full max-w-md rounded-lg border bg-muted/30 p-3 text-left">
                <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                  <Fingerprint className="h-3.5 w-3.5" />
                  RSA SHA-256
                </div>
                <code className="block break-all font-mono text-xs text-foreground">{serverFingerprint}</code>
              </div>
              <div className="mt-1 flex items-center gap-2.5">
                <Button variant="outline" size="sm" data-testid="vnc-verify-cancel" onClick={cancelVNCServerVerification}>
                  {t("action.cancel")}
                </Button>
                <Button size="sm" data-testid="vnc-verify-approve" onClick={approveVNCServer}>
                  {t("vnc.approveServer")}
                </Button>
              </div>
            </div>
          )}

          <div
            ref={vncContainerRef}
            tabIndex={0}
            className="h-full w-full outline-none"
            onKeyDownCapture={handleVNCKeyDownCapture}
            onPaste={handleVNCPaste}
          />
        </div>

        {fileOpen && fileSessionId && (
          <FileManagerPanel
            assetId={session?.fileSshAssetId}
            tabId={tabId}
            sessionId={fileSessionId}
            isActive
            isOpen
            width={fileWidth}
            onWidthChange={setFileWidth}
          />
        )}
      </div>

      <RemoteStatusBar
        width={resolution.width}
        height={resolution.height}
        showFit={viewMode === "fit"}
        fitLabel={t("vnc.autoFit")}
        connected={connected}
        elapsed={elapsed}
        extra={
          connected && clipboardEnabled ? (
            <span className="text-info">{t("vnc.clipboardSynced")}</span>
          ) : undefined
        }
      />
    </div>
  );
}
```

- [ ] **Step 5: MainPanel 给 VNCPanel 接 onEdit**

在 `frontend/src/components/layout/MainPanel.tsx` 里把
```tsx
                <VNCPanel tabId={tab.id} asset={asset} />
```
改为
```tsx
                <VNCPanel tabId={tab.id} asset={asset} onEdit={() => onEditAsset(asset)} />
```

- [ ] **Step 6: 跑测试确认通过**

Run: `pnpm vitest run src/__tests__/VNCPanel.test.tsx src/components/vnc/__tests__/VNCToolbar.test.tsx`
Expected: PASS（指纹 approve、文件面板、粘贴、语言切换保持连接、Ctrl+Alt+Del、剪贴板关闭后不再镜像 —— 全绿）。

- [ ] **Step 7: Commit**

```bash
git add frontend/src/types/novnc.d.ts frontend/src/components/vnc/VNCPanel.tsx frontend/src/components/layout/MainPanel.tsx frontend/src/__tests__/VNCPanel.test.tsx
git commit -m "✨ VNC 面板重排为 RDP 一致 chrome + 特殊键/分辨率/时长/剪贴板/指纹入 overlay"
```

---

### Task 8: 全量回归 + lint + 手动核验

**Files:** 无新增；跑全套校验。

- [ ] **Step 1: 前端全量单测**

Run: `pnpm vitest run`
Expected: PASS（重点确认 `rdp/`、`vnc/`、`remote/` 三处；无因废弃 i18n key 删除导致的其它测试引用失败——若有引用 `vnc.pasteText`/`vnc.scaleOn`/`vnc.scaleOff`/`vnc.fileChannelDisabled` 的其它测试，一并更新）。

- [ ] **Step 2: lint 零警告**

Run: `pnpm lint`
Expected: PASS（无未使用 import；无 react-x set-state-in-effect 报错）。

- [ ] **Step 3: 手动核验（对照 mockup）**

参考 `docs/superpowers/mockups/vnc-redesign.html`，启动应用连一个 VNC 资产，逐项确认：顶栏状态胶囊 / host:port、适应↔原始、特殊键菜单（Ctrl+Alt+Del / Alt+Tab / Esc）、剪贴板开关与状态栏「已同步」、文件面板开合（无通道时置灰）、全屏、断开→overlay 重连/编辑、指纹校验 overlay、底部分辨率 + 运行时长。RDP 资产回归一遍确认 chrome 无视觉/行为回归。

- [ ] **Step 4: 最终提交（若手动核验有微调）**

```bash
git add -A
git commit -m "✅ VNC UI 重设计回归通过"
```

---

## Self-Review

**1. Spec coverage（对照设计文档各节）：**
- RDP 一致 chrome（顶栏/overlay/状态栏）→ Task 6（toolbar）+ Task 7（panel 组装）+ Task 2–4（原子）。✅
- 共享原子 + 重构 RDP → Task 1–5。✅
- 适应/原始 Segmented → Task 6 + Task 7（`viewMode`/scaleViewport effect）。✅
- 特殊键（CAD/Alt+Tab/Esc，与 RDP 三项一致）→ Task 6 菜单 + Task 7 `sendSpecialKey`（`sendCtrlAltDel` + keysym）。✅
- 剪贴板前端 gate → Task 7（`clipboardEnabledRef` 于 clipboard 事件与 paste 路径）。✅
- 文件按钮（无通道禁用 + tooltip，删除常驻提示条）→ Task 6（`filesEnabled`）+ Task 7 + Task 6 Step1/2 删 `fileChannelDisabled`。✅
- 指纹校验入 overlay → Task 7（替换 ConfirmDialog）。✅
- 重连/编辑入 overlay + MainPanel onEdit → Task 4 + Task 7 Step5。✅
- 分辨率读数 → Task 3 + Task 7（读 canvas.width/height）。✅
- 运行时长 → Task 3 + Task 7（uptime ticker）。✅
- i18n（en/zh 同步、废弃键删除）→ Task 6 Step1/2。✅
- 不改后端 / 不动 transport 两阶段 → Task 7 保留原 effect。✅

**2. Placeholder scan：** 无 TBD/TODO；每个代码步骤含完整代码。✅

**3. Type consistency：** `RemoteStatus`（Task1）在 2/3/4/6/7 一致；`formatDuration` 名称一致；`VNCViewMode`/`VNCSpecialKey` 由 Task6 导出、Task7 消费一致；`RemoteConnectionOverlay` 的 props（`labels`/`reconnectTestId`/`editTestId`）在 RDP（Task5）与 VNC（Task7）调用处一致；`RemoteStatusBar` props（`showFit`/`fitLabel`/`extra`）在 Task5/Task7 一致。✅
