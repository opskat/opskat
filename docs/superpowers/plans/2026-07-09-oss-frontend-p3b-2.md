# OSS Object Browser — P3b-2 (Transfers) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the transfer layer to the OSS object browser — upload (toolbar button + drag-drop), single-object download, a live progress dock, and auto-refresh when an upload finishes into the current prefix.

**Architecture:** An OSS-specific transfer store (`ossTransferStore`, keyed by tabId, mirroring `ossBrowserStore`'s `patch`/`tabCloseHook` discipline) subscribes to the shared `transfer:progress:<id>` Wails events for each transfer started via the P3a bindings. A bottom dock renders the tab's transfers; a native-file-drop hook feeds dropped OS paths into per-file uploads; additive props on the P3b-1 breadcrumb/object-list add the upload button and per-row download. The container `OSSBrowserPanel` wires it together.

**Tech Stack:** React 19 + TypeScript, Zustand, Wails v2 runtime events (`EventsOn`/`EventsOff`/`OnFileDrop`), `@opskat/ui` (`ScrollArea`/`Button`), `lucide-react`, vitest + happy-dom.

## Global Constraints

- **Bindings are positional (no DTOs):** `OSSUploadObject(assetId: number, bucket: string, keyPrefix: string): Promise<string[]>` (native multi-select; one transferId per file; dialog-cancel → `[]`), `OSSUploadObjectPath(assetId: number, bucket: string, key: string, localPath: string): Promise<string>` (drag-drop), `OSSDownloadObject(assetId: number, bucket: string, key: string): Promise<string>` (native save dialog; dialog-cancel → `""`), `OSSCancelTransfer(transferId: string): Promise<void>` (idempotent). Import from `../../wailsjs/go/oss/OSS`.
- **Progress event** `"transfer:progress:" + transferId`, payload `{ transferId, status: "progress"|"done"|"error"|"cancelled", currentFile, filesCompleted, filesTotal, bytesDone, bytesTotal, speed, error? }`. Only `"progress"` is throttled (100ms); terminal events emit once.
- **OSS emits an EXPLICIT `"cancelled"` status** — handle it directly; NEVER infer cancellation from an error substring.
- **Frontend status vocabulary** = `"active" | "done" | "error" | "cancelled"` (wire `"progress"` → `"active"`).
- **`done` rows auto-remove after 5000ms**; `error`/`cancelled` rows persist until cleared.
- **Store keyed by tabId** with a `patch`-style helper that only mutates an existing per-tab slice; register a `tabCloseHook` that `EventsOff`s every live subscription and drops the slice.
- **`OnFileDrop` is a global singleton** — go through the existing `terminalFileDropCoordinator`; NEVER call `OnFileDrop` directly. The coordinator change is **additive** (new generic target; existing terminal/file-manager targets, precedence, and tests untouched).
- **Additive-only** to P3b-1 components: `OSSBreadcrumb` gains optional `onUpload?`, `OSSObjectList` gains optional `onDownload?`. Do not reshape existing props/behavior; existing tests must stay green.
- **i18n `oss.transfer.*`** keys added up-front (en + zh-CN, lockstep, idiomatic per language).
- **Non-component i18n** uses the singleton: `import i18n from "../i18n"; i18n.t(key)` (pattern from `aiStore.ts`).
- **ENV TRAP:** `pnpm test` / `pnpm lint` rewrite `frontend/pnpm-lock.yaml` (and sometimes `frontend/pnpm-workspace.yaml`). Before every commit: `git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml`, then `git add` ONLY the task's target files, then `git status --short` to confirm. Never commit `frontend/pnpm-lock.yaml`, `frontend/pnpm-workspace.yaml`, or anything under `frontend/wailsjs/**` (generated/gitignored).
- **Gates per task (run from `frontend/`):** `pnpm test <the task's test path>` (RED then GREEN); `npx tsc -b` clean; `pnpm lint` adds no NEW errors in the task's files (a `react-refresh/only-export-components` warning on a file that co-exports a helper + component is acceptable; run `eslint --fix` on just the task's files to clear prettier line-wrap errors). A `react-refresh` warning is expected on files exporting both a hook/util and a component.
- **happy-dom deferrals (do NOT write DOM tests for these — they're manual/live-MinIO per spec §6):** native dialogs, real desktop drag-drop path delivery, real progress streaming/throttling, and the upload-cancel context-propagation check.

## File Structure

- **Create** `frontend/src/lib/formatBytes.ts` — shared `formatBytes` (lifted from `OSSObjectList`) + new `formatSpeed`.
- **Create** `frontend/src/lib/__tests__/formatBytes.test.ts`.
- **Create** `frontend/src/stores/ossTransferStore.ts` — per-tab transfer store + event subscription + auto-refresh hook.
- **Create** `frontend/src/stores/ossTransferStore.test.ts`.
- **Create** `frontend/src/components/oss/OSSTransferDock.tsx` — bottom progress dock.
- **Create** `frontend/src/components/oss/__tests__/OSSTransferDock.test.tsx`.
- **Create** `frontend/src/components/oss/useOssFileDrop.ts` — native-drop hook (mirrors `useNativeFileDrop`).
- **Create** `frontend/src/components/oss/__tests__/useOssFileDrop.test.tsx`.
- **Modify** `frontend/src/components/oss/OSSObjectList.tsx` — re-export `formatBytes` from the shared lib; add optional `onDownload?` + a per-object download button/column.
- **Modify** `frontend/src/components/oss/OSSBreadcrumb.tsx` — add optional `onUpload?` + an upload button.
- **Modify** `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx` / `OSSBreadcrumb.test.tsx` — add the additive-prop tests.
- **Modify** `frontend/src/components/terminal/terminalFileDropCoordinator.ts` — additive `registerFileDropTarget` + `genericTargets` map (existing exports untouched).
- **Modify** `frontend/src/components/terminal/terminalFileDropCoordinator.test.ts` (path: check the real location; it's `frontend/src/__tests__/terminalFileDropCoordinator.test.ts`) — add one generic-path test.
- **Modify** `frontend/src/components/query/OSSBrowserPanel.tsx` — render the dock + drop mask, wire upload/download handlers.
- **Modify** `frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx` — add dock/upload/download wiring tests.
- **Modify** `frontend/src/i18n/locales/en/common.json` + `frontend/src/i18n/locales/zh-CN/common.json` — add `oss.transfer.*`.

---

### Task 1: Shared `formatBytes` + `formatSpeed`

**Files:**
- Create: `frontend/src/lib/formatBytes.ts`
- Create: `frontend/src/lib/__tests__/formatBytes.test.ts`
- Modify: `frontend/src/components/oss/OSSObjectList.tsx` (replace the local `formatBytes` definition with a re-export from the shared lib)

**Interfaces:**
- Produces: `formatBytes(size: number): string`, `formatSpeed(bytesPerSec: number): string`.
- Consumes: nothing (pure).

**Why the re-export:** `OSSObjectList.test.tsx` imports `formatBytes` from `"../OSSObjectList"`. Keep a re-export so that test stays green; new code imports from `@/lib/formatBytes`.

- [ ] **Step 1: Write the failing test** — `frontend/src/lib/__tests__/formatBytes.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { formatBytes, formatSpeed } from "../formatBytes";

describe("formatBytes", () => {
  it("formats bytes / KB / MB with one decimal above 1 KB", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1048576)).toBe("1.0 MB");
  });
});

describe("formatSpeed", () => {
  it("appends /s to a byte size", () => {
    expect(formatSpeed(0)).toBe("0 B/s");
    expect(formatSpeed(2048)).toBe("2.0 KB/s");
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/lib/__tests__/formatBytes.test.ts`. Expected: `Failed to resolve import "../formatBytes"`.

- [ ] **Step 3: Implement** — `frontend/src/lib/formatBytes.ts`:

```ts
/** 人类可读字节数：<1KiB 用整数 B，其余 1 位小数，单位阶梯 B/KB/MB/GB/TB。 */
export function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let val = size / 1024;
  let i = 0;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(1)} ${units[i]}`;
}

/** 传输速率：formatBytes + "/s"。 */
export function formatSpeed(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`;
}
```

- [ ] **Step 4: Re-point `OSSObjectList`** — in `frontend/src/components/oss/OSSObjectList.tsx`, DELETE the local `export function formatBytes(...) {...}` block and instead re-export from the shared lib. Add near the top imports:

```ts
export { formatBytes } from "@/lib/formatBytes";
```

Leave `shouldLoadNextPage` and everything else in `OSSObjectList.tsx` unchanged. Inside the component, `formatBytes(o.size)` now resolves to the re-exported symbol (same name, same signature) — no call-site change needed.

- [ ] **Step 5: Run — passes** — `pnpm test src/lib/__tests__/formatBytes.test.ts src/components/oss/__tests__/OSSObjectList.test.tsx`, then `npx tsc -b`, then `pnpm lint` (fix only these files if needed).

- [ ] **Step 6: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/lib/formatBytes.ts frontend/src/lib/__tests__/formatBytes.test.ts frontend/src/components/oss/OSSObjectList.tsx
git status --short
git commit -m "♻️ 抽取 formatBytes 到共享 lib 并新增 formatSpeed"
```

---

### Task 2: i18n `oss.transfer.*` (en / zh-CN lockstep)

**Files:**
- Modify: `frontend/src/i18n/locales/en/common.json`
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`

**Interfaces:**
- Produces: the `oss.transfer.*` key namespace (10 keys) consumed by Tasks 5–8.

Add a `transfer` object inside the existing `oss` object (sibling of `oss.browser` / `oss.form`) in BOTH locale files, with identical key sets. Keys and values:

- [ ] **Step 1: Add to `en/common.json`** (inside `"oss": { ... }`, alongside `"browser"`):

```json
"transfer": {
  "upload": "Upload",
  "download": "Download",
  "transfers": "Transfers",
  "clearCompleted": "Clear completed",
  "cancel": "Cancel",
  "clear": "Clear",
  "dropHint": "Drop files to upload",
  "uploadFailed": "Upload failed",
  "downloadFailed": "Download failed",
  "refreshAfterUploadFailed": "Uploaded, but refreshing the list failed"
}
```

- [ ] **Step 2: Add to `zh-CN/common.json`** (same location, same keys):

```json
"transfer": {
  "upload": "上传",
  "download": "下载",
  "transfers": "传输",
  "clearCompleted": "清除已完成",
  "cancel": "取消",
  "clear": "清除",
  "dropHint": "拖入文件以上传",
  "uploadFailed": "上传失败",
  "downloadFailed": "下载失败",
  "refreshAfterUploadFailed": "已上传，但刷新列表失败"
}
```

- [ ] **Step 3: Run — passes** — `pnpm test src/__tests__/i18n.test.ts`. Expected: GREEN (en⇔zh parity holds; unused-yet keys are allowed). If it reports a missing/asymmetric key, fix the JSON.

- [ ] **Step 4: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git status --short
git commit -m "🌐 新增 OSS 传输文案 oss.transfer.*"
```

---

### Task 3: `ossTransferStore.ts` (per-tab transfer store + subscription + auto-refresh)

**Files:**
- Create: `frontend/src/stores/ossTransferStore.ts`
- Create: `frontend/src/stores/ossTransferStore.test.ts`

**Interfaces:**
- Consumes: the 4 transfer bindings; `EventsOn`/`EventsOff` from `../../wailsjs/runtime/runtime`; `registerTabCloseHook` + `QueryTabMeta` from `./tabStore`; `useOssBrowserStore` from `./ossBrowserStore` (for `.getState().tabs[tabId]?.currentPrefix` and `.refresh(tabId)`); `i18n` from `../i18n`; `toast` from `sonner`.
- Produces:
  ```ts
  export type OssTransferStatus = "active" | "done" | "error" | "cancelled";
  export interface OssTransfer { transferId: string; tabId: string; direction: "upload"|"download"; name: string; targetPrefix?: string; bytesDone: number; bytesTotal: number; speed: number; status: OssTransferStatus; error?: string; }
  export const useOssTransferStore // zustand store
  // actions: startUpload(tabId, assetId, bucket, prefix), startUploadPath(tabId, assetId, bucket, prefix, localPath),
  //          startDownload(tabId, assetId, bucket, key), cancel(transferId), clear(tabId, transferId), clearCompleted(tabId)
  ```

- [ ] **Step 1: Write the failing test** — `frontend/src/stores/ossTransferStore.test.ts`:

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { OSSUploadObject, OSSDownloadObject, OSSCancelTransfer } from "../../wailsjs/go/oss/OSS";
import { useOssTransferStore } from "./ossTransferStore";
import { useOssBrowserStore } from "./ossBrowserStore";

const TAB = "query-9";

// Grab the progress handler that subscribeProgress registered for a given transferId.
function handlerFor(transferId: string): (e: unknown) => void {
  const call = vi.mocked(EventsOn).mock.calls.find((c) => c[0] === "transfer:progress:" + transferId);
  if (!call) throw new Error("no EventsOn for " + transferId);
  return call[1] as (e: unknown) => void;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(EventsOn).mockClear();
  vi.mocked(EventsOff).mockClear();
  vi.mocked(OSSUploadObject).mockReset();
  vi.mocked(OSSDownloadObject).mockReset();
  vi.mocked(OSSCancelTransfer).mockReset().mockResolvedValue(undefined);
  useOssTransferStore.setState({ tabs: {} });
  useOssBrowserStore.setState({ tabs: {} });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("ossTransferStore", () => {
  it("startUpload subscribes one active row per returned transferId", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1", "oss-2"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    const rows = useOssTransferStore.getState().tabs[TAB].transfers;
    expect(Object.keys(rows)).toEqual(["oss-1", "oss-2"]);
    expect(rows["oss-1"].status).toBe("active");
    expect(rows["oss-1"].direction).toBe("upload");
    expect(rows["oss-1"].targetPrefix).toBe("docs/");
    expect(EventsOn).toHaveBeenCalledWith("transfer:progress:oss-1", expect.any(Function));
  });

  it("startUpload with an empty array (dialog cancel) adds no rows", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue([] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    expect(useOssTransferStore.getState().tabs[TAB]?.transfers ?? {}).toEqual({});
  });

  it("a progress event merges numeric fields and keeps status active", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "progress", currentFile: "/x/a.txt", bytesDone: 50, bytesTotal: 100, speed: 25, filesCompleted: 0, filesTotal: 1 });
    const row = useOssTransferStore.getState().tabs[TAB].transfers["oss-1"];
    expect(row.status).toBe("active");
    expect(row.bytesDone).toBe(50);
    expect(row.speed).toBe(25);
    expect(row.name).toBe("a.txt");
  });

  it("a done event marks done, EventsOff, and auto-removes after 5s", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "done", currentFile: "", bytesDone: 100, bytesTotal: 100, speed: 0, filesCompleted: 1, filesTotal: 1 });
    expect(useOssTransferStore.getState().tabs[TAB].transfers["oss-1"].status).toBe("done");
    expect(EventsOff).toHaveBeenCalledWith("transfer:progress:oss-1");
    vi.advanceTimersByTime(5000);
    expect(useOssTransferStore.getState().tabs[TAB].transfers["oss-1"]).toBeUndefined();
  });

  it("on upload done, refreshes the browser only when targetPrefix === currentPrefix", async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    useOssBrowserStore.setState({ tabs: { [TAB]: { currentPrefix: "docs/" } as never }, refresh } as never);
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "done", currentFile: "", bytesDone: 1, bytesTotal: 1, speed: 0, filesCompleted: 1, filesTotal: 1 });
    expect(refresh).toHaveBeenCalledWith(TAB);
  });

  it("on upload done, does NOT refresh when the user navigated away", async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    useOssBrowserStore.setState({ tabs: { [TAB]: { currentPrefix: "other/" } as never }, refresh } as never);
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "done", currentFile: "", bytesDone: 1, bytesTotal: 1, speed: 0, filesCompleted: 1, filesTotal: 1 });
    expect(refresh).not.toHaveBeenCalled();
  });

  it("an explicit cancelled event sets status cancelled (no substring inference)", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "cancelled", currentFile: "", bytesDone: 0, bytesTotal: 0, speed: 0, filesCompleted: 0, filesTotal: 0 });
    expect(useOssTransferStore.getState().tabs[TAB].transfers["oss-1"].status).toBe("cancelled");
    expect(EventsOff).toHaveBeenCalledWith("transfer:progress:oss-1");
  });

  it("an error event sets status error with the message", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "error", error: "boom", currentFile: "", bytesDone: 0, bytesTotal: 0, speed: 0, filesCompleted: 0, filesTotal: 0 });
    const row = useOssTransferStore.getState().tabs[TAB].transfers["oss-1"];
    expect(row.status).toBe("error");
    expect(row.error).toBe("boom");
  });

  it("startDownload with an empty id (dialog cancel) adds no rows", async () => {
    vi.mocked(OSSDownloadObject).mockResolvedValue("" as never);
    await useOssTransferStore.getState().startDownload(TAB, 9, "b", "docs/a.txt");
    expect(useOssTransferStore.getState().tabs[TAB]?.transfers ?? {}).toEqual({});
  });

  it("startDownload with an id adds a download row named after the key basename", async () => {
    vi.mocked(OSSDownloadObject).mockResolvedValue("oss-d1" as never);
    await useOssTransferStore.getState().startDownload(TAB, 9, "b", "docs/a.txt");
    const row = useOssTransferStore.getState().tabs[TAB].transfers["oss-d1"];
    expect(row.direction).toBe("download");
    expect(row.name).toBe("a.txt");
  });

  it("cancel calls OSSCancelTransfer", () => {
    useOssTransferStore.getState().cancel("oss-1");
    expect(OSSCancelTransfer).toHaveBeenCalledWith("oss-1");
  });

  it("clearCompleted keeps only active rows", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1", "oss-2"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({ transferId: "oss-1", status: "error", error: "x", currentFile: "", bytesDone: 0, bytesTotal: 0, speed: 0, filesCompleted: 0, filesTotal: 0 });
    useOssTransferStore.getState().clearCompleted(TAB);
    const ids = Object.keys(useOssTransferStore.getState().tabs[TAB].transfers);
    expect(ids).toEqual(["oss-2"]);
  });

  it("keeps two tabs isolated", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-a"] as never);
    await useOssTransferStore.getState().startUpload("tab-A", 1, "b", "");
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-b"] as never);
    await useOssTransferStore.getState().startUpload("tab-B", 2, "b", "");
    expect(Object.keys(useOssTransferStore.getState().tabs["tab-A"].transfers)).toEqual(["oss-a"]);
    expect(Object.keys(useOssTransferStore.getState().tabs["tab-B"].transfers)).toEqual(["oss-b"]);
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/stores/ossTransferStore.test.ts`. Expected: `Failed to resolve import "./ossTransferStore"`.

- [ ] **Step 3: Implement** — `frontend/src/stores/ossTransferStore.ts`:

```ts
import { create } from "zustand";
import { toast } from "sonner";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import {
  OSSUploadObject,
  OSSUploadObjectPath,
  OSSDownloadObject,
  OSSCancelTransfer,
} from "../../wailsjs/go/oss/OSS";
import { registerTabCloseHook, type QueryTabMeta } from "./tabStore";
import { useOssBrowserStore } from "./ossBrowserStore";
import i18n from "../i18n";

const DONE_LINGER_MS = 5000;

export type OssTransferStatus = "active" | "done" | "error" | "cancelled";

export interface OssTransfer {
  transferId: string;
  tabId: string;
  direction: "upload" | "download";
  name: string;
  targetPrefix?: string;
  bytesDone: number;
  bytesTotal: number;
  speed: number;
  status: OssTransferStatus;
  error?: string;
}

interface OssTransferProgressEvent {
  transferId: string;
  status: "progress" | "done" | "error" | "cancelled";
  currentFile: string;
  filesCompleted: number;
  filesTotal: number;
  bytesDone: number;
  bytesTotal: number;
  speed: number;
  error?: string;
}

interface OssTransferTabState {
  transfers: Record<string, OssTransfer>;
}

interface OssTransferState {
  tabs: Record<string, OssTransferTabState>;
  startUpload: (tabId: string, assetId: number, bucket: string, prefix: string) => Promise<void>;
  startUploadPath: (tabId: string, assetId: number, bucket: string, prefix: string, localPath: string) => Promise<void>;
  startDownload: (tabId: string, assetId: number, bucket: string, key: string) => Promise<void>;
  cancel: (transferId: string) => void;
  clear: (tabId: string, transferId: string) => void;
  clearCompleted: (tabId: string) => void;
}

/** 取路径末段（兼容 / 和 \，去掉结尾分隔符）。 */
function basename(p: string): string {
  const trimmed = p.replace(/[/\\]+$/, "");
  const i = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return i >= 0 ? trimmed.slice(i + 1) : trimmed;
}

export const useOssTransferStore = create<OssTransferState>((set, get) => {
  const addTransfer = (t: OssTransfer) =>
    set((s) => {
      const tab = s.tabs[t.tabId] ?? { transfers: {} };
      return { tabs: { ...s.tabs, [t.tabId]: { transfers: { ...tab.transfers, [t.transferId]: t } } } };
    });

  // 只对已存在的 tab+transfer 打补丁（tab 关闭 / 已被清理后不重建）。
  const patchTransfer = (tabId: string, transferId: string, fn: (t: OssTransfer) => OssTransfer) =>
    set((s) => {
      const tab = s.tabs[tabId];
      if (!tab || !tab.transfers[transferId]) return {};
      return {
        tabs: { ...s.tabs, [tabId]: { transfers: { ...tab.transfers, [transferId]: fn(tab.transfers[transferId]) } } },
      };
    });

  const removeTransfer = (tabId: string, transferId: string) =>
    set((s) => {
      const tab = s.tabs[tabId];
      if (!tab) return {};
      const transfers = { ...tab.transfers };
      delete transfers[transferId];
      return { tabs: { ...s.tabs, [tabId]: { transfers } } };
    });

  // 上传完成后：若目标前缀 === 当前浏览前缀，则刷新对象列表（store→store，via getState）。
  const maybeRefreshAfterUpload = (tabId: string, targetPrefix?: string) => {
    const browser = useOssBrowserStore.getState();
    const current = browser.tabs[tabId]?.currentPrefix;
    if (targetPrefix === undefined || current === undefined || targetPrefix !== current) return;
    void browser.refresh(tabId).catch(() => {
      toast.error(i18n.t("oss.transfer.refreshAfterUploadFailed"));
    });
  };

  const subscribeProgress = (tabId: string, transferId: string) => {
    const eventName = "transfer:progress:" + transferId;
    EventsOn(eventName, (e: OssTransferProgressEvent) => {
      if (!get().tabs[tabId]?.transfers[transferId]) return;
      switch (e.status) {
        case "progress":
          patchTransfer(tabId, transferId, (t) => ({
            ...t,
            name: e.currentFile ? basename(e.currentFile) : t.name,
            bytesDone: e.bytesDone,
            bytesTotal: e.bytesTotal,
            speed: e.speed,
          }));
          break;
        case "done": {
          patchTransfer(tabId, transferId, (t) => ({ ...t, status: "done", bytesDone: t.bytesTotal || t.bytesDone }));
          EventsOff(eventName);
          const done = get().tabs[tabId]?.transfers[transferId];
          if (done?.direction === "upload") maybeRefreshAfterUpload(tabId, done.targetPrefix);
          setTimeout(() => {
            if (get().tabs[tabId]?.transfers[transferId]?.status === "done") removeTransfer(tabId, transferId);
          }, DONE_LINGER_MS);
          break;
        }
        case "cancelled":
          // OSS 显式发 "cancelled"（不像 SFTP 从错误子串推断）。
          patchTransfer(tabId, transferId, (t) => ({ ...t, status: "cancelled" }));
          EventsOff(eventName);
          break;
        case "error":
          patchTransfer(tabId, transferId, (t) => ({ ...t, status: "error", error: e.error }));
          EventsOff(eventName);
          break;
      }
    });
  };

  return {
    tabs: {},

    startUpload: async (tabId, assetId, bucket, prefix) => {
      const ids = await OSSUploadObject(assetId, bucket, prefix); // 空数组 = 用户取消对话框
      for (const id of ids) {
        addTransfer({ transferId: id, tabId, direction: "upload", name: "", targetPrefix: prefix, bytesDone: 0, bytesTotal: 0, speed: 0, status: "active" });
        subscribeProgress(tabId, id);
      }
    },

    startUploadPath: async (tabId, assetId, bucket, prefix, localPath) => {
      const name = basename(localPath);
      const id = await OSSUploadObjectPath(assetId, bucket, prefix + name, localPath);
      if (!id) return;
      addTransfer({ transferId: id, tabId, direction: "upload", name, targetPrefix: prefix, bytesDone: 0, bytesTotal: 0, speed: 0, status: "active" });
      subscribeProgress(tabId, id);
    },

    startDownload: async (tabId, assetId, bucket, key) => {
      const id = await OSSDownloadObject(assetId, bucket, key);
      if (!id) return; // 空串 = 用户取消保存对话框
      addTransfer({ transferId: id, tabId, direction: "download", name: basename(key), bytesDone: 0, bytesTotal: 0, speed: 0, status: "active" });
      subscribeProgress(tabId, id);
    },

    cancel: (transferId) => {
      void OSSCancelTransfer(transferId);
    },

    clear: (tabId, transferId) => removeTransfer(tabId, transferId),

    clearCompleted: (tabId) =>
      set((s) => {
        const tab = s.tabs[tabId];
        if (!tab) return {};
        const transfers: Record<string, OssTransfer> = {};
        for (const [id, t] of Object.entries(tab.transfers)) {
          if (t.status === "active") transfers[id] = t;
        }
        return { tabs: { ...s.tabs, [tabId]: { transfers } } };
      }),
  };
});

// tab 关闭时退订所有事件并清理该 OSS query tab 的传输态。
registerTabCloseHook((tab) => {
  if (tab.type !== "query") return;
  if ((tab.meta as QueryTabMeta).assetType !== "oss") return;
  useOssTransferStore.setState((s) => {
    const tabState = s.tabs[tab.id];
    if (tabState) {
      for (const id of Object.keys(tabState.transfers)) EventsOff("transfer:progress:" + id);
    }
    const next = { ...s.tabs };
    delete next[tab.id];
    return { tabs: next };
  });
});
```

- [ ] **Step 4: Run — passes** — `pnpm test src/stores/ossTransferStore.test.ts`, then `npx tsc -b`, then `pnpm lint` (fix only this file if needed).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/stores/ossTransferStore.ts frontend/src/stores/ossTransferStore.test.ts
git status --short
git commit -m "✨ OSS 传输 store（按 tab 隔离 + 进度订阅 + 上传后自动刷新）"
```

---

### Task 4: Additive generic drop target on the file-drop coordinator

**Files:**
- Modify: `frontend/src/components/terminal/terminalFileDropCoordinator.ts`
- Modify: `frontend/src/__tests__/terminalFileDropCoordinator.test.ts` (confirm the actual path first via `ls`; the coordinator's test lives under `frontend/src/__tests__/`)

**Interfaces:**
- Produces: `registerFileDropTarget(target: { getRect: () => DOMRect | null | undefined; onDrop: (paths: string[]) => void }): () => void`.
- Consumes: the existing `RectProvider`, `firstHit`, `syncWailsFileDropListener` internals (untouched).

**Constraint:** ADDITIVE ONLY. Do not touch `terminalTargets`, `fileManagerTargets`, their register fns, or the existing hit-test precedence. The generic target is checked LAST (after file-manager and terminal).

- [ ] **Step 1: Write the failing test** — append to `frontend/src/__tests__/terminalFileDropCoordinator.test.ts` (adapt imports to match the file's existing style; it already imports `OnFileDrop`/`OnFileDropOff` mocks and `resetTerminalFileDropCoordinatorForTest`):

```ts
import { registerFileDropTarget } from "../components/terminal/terminalFileDropCoordinator";

describe("registerFileDropTarget (generic)", () => {
  it("routes a drop within the target's rect to onDrop, and toggles OnFileDrop on/off", () => {
    const onDrop = vi.fn();
    const rect = { left: 0, top: 0, right: 100, bottom: 100 } as DOMRect;
    const unregister = registerFileDropTarget({ getRect: () => rect, onDrop });
    expect(OnFileDrop).toHaveBeenCalledTimes(1);
    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    handler(50, 50, ["/a/b.txt", "/c/d.txt"]);
    expect(onDrop).toHaveBeenCalledWith(["/a/b.txt", "/c/d.txt"]);
    handler(500, 500, ["/x.txt"]); // outside the rect
    expect(onDrop).toHaveBeenCalledTimes(1);
    unregister();
    expect(OnFileDropOff).toHaveBeenCalled();
  });
});
```

(Keep the file's existing `beforeEach`/`afterEach` that call `resetTerminalFileDropCoordinatorForTest()`; if that reset doesn't yet clear generic targets, Step 3 fixes it.)

- [ ] **Step 2: Run — fails** — `pnpm test src/__tests__/terminalFileDropCoordinator.test.ts`. Expected: `registerFileDropTarget is not a function` (not exported yet).

- [ ] **Step 3: Implement** — in `frontend/src/components/terminal/terminalFileDropCoordinator.ts`:

  1. Add the generic map beside the existing two:
  ```ts
  const genericTargets = new Map<symbol, { getRect: RectProvider; onDrop: (paths: string[]) => void }>();
  ```
  2. Include it in `targetCount()`:
  ```ts
  function targetCount(): number {
    return terminalTargets.size + fileManagerTargets.size + genericTargets.size;
  }
  ```
  3. Rewrite `handleFileDrop` so precedence is file-manager → terminal → generic (each returns on hit):
  ```ts
  function handleFileDrop(x: number, y: number, paths: string[]) {
    const fileManagerTarget = firstHit(fileManagerTargets.values(), x, y);
    if (fileManagerTarget) {
      const remoteDir = fileManagerTarget.getRemoteDir();
      for (const path of paths) fileManagerTarget.startUploadFile(path, remoteDir);
      return;
    }
    const terminalTarget = firstHit(terminalTargets.values(), x, y);
    if (terminalTarget) {
      terminalTarget.uploadFiles(paths);
      return;
    }
    const genericTarget = firstHit(genericTargets.values(), x, y);
    genericTarget?.onDrop(paths);
  }
  ```
  4. Add the exported registrar (mirrors the existing two):
  ```ts
  export function registerFileDropTarget(target: { getRect: RectProvider; onDrop: (paths: string[]) => void }): () => void {
    const id = Symbol("generic-file-drop-target");
    genericTargets.set(id, target);
    syncWailsFileDropListener();
    return () => {
      genericTargets.delete(id);
      syncWailsFileDropListener();
    };
  }
  ```
  5. Clear it in the test reset:
  ```ts
  export function resetTerminalFileDropCoordinatorForTest() {
    terminalTargets.clear();
    fileManagerTargets.clear();
    genericTargets.clear();
    if (listening) {
      OnFileDropOff();
      listening = false;
    }
  }
  ```

- [ ] **Step 4: Run — passes** — `pnpm test src/__tests__/terminalFileDropCoordinator.test.ts` (the new test AND all existing terminal/file-manager tests green), then `npx tsc -b`, then `pnpm lint`.

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/terminal/terminalFileDropCoordinator.ts frontend/src/__tests__/terminalFileDropCoordinator.test.ts
git status --short
git commit -m "✨ 文件拖放协调器新增通用 registerFileDropTarget"
```

---

### Task 5: `OSSTransferDock.tsx` (bottom progress dock)

**Files:**
- Create: `frontend/src/components/oss/OSSTransferDock.tsx`
- Create: `frontend/src/components/oss/__tests__/OSSTransferDock.test.tsx`

**Interfaces:**
- Consumes: `OssTransfer` type from `@/stores/ossTransferStore`; `formatBytes`/`formatSpeed` from `@/lib/formatBytes`; `@opskat/ui` `ScrollArea`/`Button`; `lucide-react`; `oss.transfer.*` i18n.
- Produces:
  ```ts
  export interface OSSTransferDockProps { transfers: OssTransfer[]; onCancel: (transferId: string) => void; onClear: (transferId: string) => void; onClearCompleted: () => void; }
  export function OSSTransferDock(props: OSSTransferDockProps): JSX.Element
  ```

- [ ] **Step 1: Write the failing test** — `frontend/src/components/oss/__tests__/OSSTransferDock.test.tsx`:

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSTransferDock } from "../OSSTransferDock";
import type { OssTransfer } from "@/stores/ossTransferStore";

function tx(over: Partial<OssTransfer>): OssTransfer {
  return { transferId: "t1", tabId: "q", direction: "upload", name: "a.txt", bytesDone: 50, bytesTotal: 100, speed: 1024, status: "active", ...over };
}

describe("OSSTransferDock", () => {
  it("renders a row with name, speed and percentage; the button cancels an active row", () => {
    const onCancel = vi.fn();
    const onClear = vi.fn();
    render(<OSSTransferDock transfers={[tx({})]} onCancel={onCancel} onClear={onClear} onClearCompleted={vi.fn()} />);
    expect(screen.getByText("a.txt")).toBeInTheDocument();
    expect(screen.getByText("1.0 KB/s")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("oss-transfer-action-t1"));
    expect(onCancel).toHaveBeenCalledWith("t1");
    expect(onClear).not.toHaveBeenCalled();
  });

  it("the row button clears a finished row instead of cancelling", () => {
    const onCancel = vi.fn();
    const onClear = vi.fn();
    render(<OSSTransferDock transfers={[tx({ transferId: "t2", status: "done" })]} onCancel={onCancel} onClear={onClear} onClearCompleted={vi.fn()} />);
    fireEvent.click(screen.getByTestId("oss-transfer-action-t2"));
    expect(onClear).toHaveBeenCalledWith("t2");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("clear-completed header button fires onClearCompleted", () => {
    const onClearCompleted = vi.fn();
    render(<OSSTransferDock transfers={[tx({})]} onCancel={vi.fn()} onClear={vi.fn()} onClearCompleted={onClearCompleted} />);
    fireEvent.click(screen.getByTestId("oss-transfer-clear-completed"));
    expect(onClearCompleted).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/OSSTransferDock.test.tsx`. Expected: `Failed to resolve import "../OSSTransferDock"`.

- [ ] **Step 3: Implement** — `frontend/src/components/oss/OSSTransferDock.tsx`:

```tsx
import { useTranslation } from "react-i18next";
import { ScrollArea, Button } from "@opskat/ui";
import { Upload, Download, Loader2, CheckCircle2, XCircle, X } from "lucide-react";
import { formatBytes, formatSpeed } from "@/lib/formatBytes";
import type { OssTransfer } from "@/stores/ossTransferStore";

export interface OSSTransferDockProps {
  transfers: OssTransfer[];
  onCancel: (transferId: string) => void;
  onClear: (transferId: string) => void;
  onClearCompleted: () => void;
}

export function OSSTransferDock({ transfers, onCancel, onClear, onClearCompleted }: OSSTransferDockProps) {
  const { t } = useTranslation();
  return (
    <div className="border-t bg-muted/10" data-testid="oss-transfer-dock">
      <div className="flex items-center justify-between px-3 py-1 text-xs text-muted-foreground">
        <span>
          {t("oss.transfer.transfers")} ({transfers.length})
        </span>
        <Button size="sm" variant="ghost" onClick={onClearCompleted} data-testid="oss-transfer-clear-completed">
          {t("oss.transfer.clearCompleted")}
        </Button>
      </div>
      <ScrollArea className="max-h-32">
        {transfers.map((tr) => {
          const percent = tr.bytesTotal ? Math.round((tr.bytesDone / tr.bytesTotal) * 100) : 0;
          const DirIcon = tr.direction === "upload" ? Upload : Download;
          const StatusIcon = tr.status === "active" ? Loader2 : tr.status === "done" ? CheckCircle2 : XCircle;
          const active = tr.status === "active";
          return (
            <div key={tr.transferId} className="flex items-center gap-2 px-3 py-1 text-xs" data-testid={`oss-transfer-row-${tr.transferId}`}>
              <DirIcon className="size-3 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate" title={tr.error ?? tr.name}>
                {tr.name}
              </span>
              <div className="h-1 w-24 shrink-0 overflow-hidden rounded-full bg-muted">
                <div className="h-full bg-primary" style={{ width: `${percent}%` }} />
              </div>
              <span className="w-16 shrink-0 text-right text-muted-foreground">{formatSpeed(tr.speed)}</span>
              <span className="w-24 shrink-0 text-right text-muted-foreground">
                {formatBytes(tr.bytesDone)}/{formatBytes(tr.bytesTotal)}
              </span>
              <StatusIcon className={`size-3 shrink-0 ${active ? "animate-spin text-muted-foreground" : ""}`} />
              <Button
                size="sm"
                variant="ghost"
                className="size-5 shrink-0 p-0"
                onClick={() => (active ? onCancel(tr.transferId) : onClear(tr.transferId))}
                title={active ? t("oss.transfer.cancel") : t("oss.transfer.clear")}
                data-testid={`oss-transfer-action-${tr.transferId}`}
              >
                <X className="size-3" />
              </Button>
            </div>
          );
        })}
      </ScrollArea>
    </div>
  );
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/OSSTransferDock.test.tsx`, then `npx tsc -b`, then `pnpm lint` (`eslint --fix` this file for prettier if needed).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/OSSTransferDock.tsx frontend/src/components/oss/__tests__/OSSTransferDock.test.tsx
git status --short
git commit -m "✨ OSS 传输进度 dock"
```

---

### Task 6: Additive entry-point props — download on `OSSObjectList`, upload on `OSSBreadcrumb`

**Files:**
- Modify: `frontend/src/components/oss/OSSObjectList.tsx`
- Modify: `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx`
- Modify: `frontend/src/components/oss/OSSBreadcrumb.tsx`
- Modify: `frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx`

**Interfaces:**
- Produces: `OSSObjectListProps` gains `onDownload?: (key: string) => void`; `OSSBreadcrumbProps` gains `onUpload?: () => void`. Both optional and additive — all existing props/behavior/tests unchanged.

- [ ] **Step 1: Add the failing tests.**

  Append to `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx` (inside the `OSSObjectList` describe; reuse its existing `base`/`obj` helpers):
  ```tsx
  it("shows a download button on object rows that fires onDownload", () => {
    const onDownload = vi.fn();
    render(<OSSObjectList {...base} onDownload={onDownload} prefixes={["docs/sub/"]} objects={[obj("docs/a.txt", 1)]} />);
    fireEvent.click(screen.getByTestId("oss-download-docs/a.txt"));
    expect(onDownload).toHaveBeenCalledWith("docs/a.txt");
    // folders get no download button
    expect(screen.queryByTestId("oss-download-docs/sub/")).toBeNull();
  });
  ```

  Append to `frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx` (inside the `OSSBreadcrumb` describe):
  ```tsx
  it("renders an upload button that fires onUpload when provided", () => {
    const onUpload = vi.fn();
    render(<OSSBreadcrumb bucket="mb" prefix="" onNavigate={vi.fn()} onRefresh={vi.fn()} onUpload={onUpload} />);
    fireEvent.click(screen.getByTestId("oss-upload"));
    expect(onUpload).toHaveBeenCalledTimes(1);
  });
  ```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/OSSObjectList.test.tsx src/components/oss/__tests__/OSSBreadcrumb.test.tsx`. Expected: missing `oss-download-*` / `oss-upload` testids.

- [ ] **Step 3a: Implement the download action** in `OSSObjectList.tsx`:
  - Add to the props interface: `onDownload?: (key: string) => void;` and destructure it.
  - Import `Download` from `lucide-react` (add to the existing lucide import).
  - Add an always-present trailing action column so folder/object rows stay aligned. In the `<thead>` row add a final header cell: `<th className="w-8 px-2 py-1" />`. In the folder `<tr>` add a trailing empty cell: `<td className="px-2 py-1" />`. Add the `group` class to the object `<tr>` (`className="group hover:bg-accent/50"`). In the object `<tr>`, add a trailing cell:
  ```tsx
  <td className="px-2 py-1 text-right">
    {onDownload && (
      <button
        type="button"
        className="opacity-0 group-hover:opacity-100"
        onClick={() => onDownload(o.key)}
        title={t("oss.transfer.download")}
        data-testid={`oss-download-${o.key}`}
      >
        <Download className="size-3" />
      </button>
    )}
  </td>
  ```

- [ ] **Step 3b: Implement the upload button** in `OSSBreadcrumb.tsx`:
  - Add to the props interface: `onUpload?: () => void;` and destructure it.
  - Import `Upload` from `lucide-react` (add alongside the existing `RefreshCw` import).
  - In the action area (next to the existing refresh `Button`), add before the refresh button:
  ```tsx
  {onUpload && (
    <Button size="sm" variant="outline" className="shrink-0" onClick={onUpload} data-testid="oss-upload">
      <Upload className="size-3" /> {t("oss.transfer.upload")}
    </Button>
  )}
  ```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/OSSObjectList.test.tsx src/components/oss/__tests__/OSSBreadcrumb.test.tsx`, then `npx tsc -b`, then `pnpm lint` (`eslint --fix` the two component files if needed).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/OSSObjectList.tsx frontend/src/components/oss/__tests__/OSSObjectList.test.tsx frontend/src/components/oss/OSSBreadcrumb.tsx frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx
git status --short
git commit -m "✨ OSS 列表行下载动作与面包屑上传按钮（附加 props）"
```

---

### Task 7: `useOssFileDrop` hook (native drop → per-file upload + drag-over visual)

**Files:**
- Create: `frontend/src/components/oss/useOssFileDrop.ts`
- Create: `frontend/src/components/oss/__tests__/useOssFileDrop.test.tsx`

**Interfaces:**
- Consumes: `registerFileDropTarget` from `@/components/terminal/terminalFileDropCoordinator` (Task 4); `useOssTransferStore` (Task 3). Mirrors the existing `useNativeFileDrop` pattern (register a coordinator target + a `MutationObserver` on the `wails-drop-target-active` class).
- Produces:
  ```ts
  export interface UseOssFileDropOptions { dropRef: RefObject<HTMLElement | null>; tabId: string; assetId: number; bucket: string; prefix: string; active: boolean; }
  export function useOssFileDrop(opts: UseOssFileDropOptions): boolean  // isDragOver
  ```

**Note:** the test mocks the coordinator module to capture the registered target and drive its `onDrop` directly (the rect/hit-test itself is Task 4's tested concern). Real desktop drag-drop is manual/live-MinIO per spec §6.

- [ ] **Step 1: Write the failing test** — `frontend/src/components/oss/__tests__/useOssFileDrop.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { useOssFileDrop } from "../useOssFileDrop";
import { registerFileDropTarget } from "@/components/terminal/terminalFileDropCoordinator";
import { useOssTransferStore } from "@/stores/ossTransferStore";

vi.mock("@/components/terminal/terminalFileDropCoordinator", () => ({
  registerFileDropTarget: vi.fn(() => vi.fn()),
}));

function Harness() {
  const ref = useRef<HTMLDivElement>(null);
  useOssFileDrop({ dropRef: ref, tabId: "q", assetId: 7, bucket: "b", prefix: "docs/", active: true });
  return <div ref={ref} />;
}

beforeEach(() => {
  vi.mocked(registerFileDropTarget).mockClear();
  useOssTransferStore.setState({ tabs: {} });
});

describe("useOssFileDrop", () => {
  it("registers a drop target whose onDrop starts one upload per dropped path", () => {
    const startUploadPath = vi.fn().mockResolvedValue(undefined);
    useOssTransferStore.setState({ startUploadPath } as never);
    render(<Harness />);
    expect(registerFileDropTarget).toHaveBeenCalledTimes(1);
    const target = vi.mocked(registerFileDropTarget).mock.calls[0][0];
    target.onDrop(["/a/one.txt", "/b/two.txt"]);
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/a/one.txt");
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/b/two.txt");
  });

  it("does not register when inactive", () => {
    function Inactive() {
      const ref = useRef<HTMLDivElement>(null);
      useOssFileDrop({ dropRef: ref, tabId: "q", assetId: 7, bucket: "b", prefix: "", active: false });
      return <div ref={ref} />;
    }
    render(<Inactive />);
    expect(registerFileDropTarget).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/useOssFileDrop.test.tsx`. Expected: `Failed to resolve import "../useOssFileDrop"`.

- [ ] **Step 3: Implement** — `frontend/src/components/oss/useOssFileDrop.ts`:

```ts
import { useEffect, useRef, useState, type RefObject } from "react";
import { registerFileDropTarget } from "@/components/terminal/terminalFileDropCoordinator";
import { useOssTransferStore } from "@/stores/ossTransferStore";

export interface UseOssFileDropOptions {
  dropRef: RefObject<HTMLElement | null>;
  tabId: string;
  assetId: number;
  bucket: string;
  prefix: string;
  active: boolean;
}

export function useOssFileDrop({ dropRef, tabId, assetId, bucket, prefix, active }: UseOssFileDropOptions): boolean {
  const [isDragOver, setIsDragOver] = useState(false);
  const startUploadPath = useOssTransferStore((s) => s.startUploadPath);

  // 用 ref 保存最新上传上下文，避免把 prefix/bucket 放进注册依赖里反复注册/退订。
  const ctx = useRef({ tabId, assetId, bucket, prefix });
  ctx.current = { tabId, assetId, bucket, prefix };

  useEffect(() => {
    if (!active) return;
    return registerFileDropTarget({
      getRect: () => dropRef.current?.getBoundingClientRect() ?? null,
      onDrop: (paths) => {
        setIsDragOver(false);
        const c = ctx.current;
        for (const p of paths) void startUploadPath(c.tabId, c.assetId, c.bucket, c.prefix, p);
      },
    });
  }, [active, dropRef, startUploadPath]);

  // Wails 在原生拖拽经过带 `--wails-drop-target: drop` 的元素时切换 wails-drop-target-active 类。
  useEffect(() => {
    const el = dropRef.current;
    if (!el || !active) return;
    const observer = new MutationObserver(() => {
      setIsDragOver(el.classList.contains("wails-drop-target-active"));
    });
    observer.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, [active, dropRef]);

  return isDragOver;
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/useOssFileDrop.test.tsx`, then `npx tsc -b`, then `pnpm lint`.

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/useOssFileDrop.ts frontend/src/components/oss/__tests__/useOssFileDrop.test.tsx
git status --short
git commit -m "✨ OSS 原生文件拖放 hook（拖入上传 + 拖拽高亮）"
```

---

### Task 8: Wire transfers into `OSSBrowserPanel`

**Files:**
- Modify: `frontend/src/components/query/OSSBrowserPanel.tsx`
- Modify: `frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx`

**Interfaces:**
- Consumes: `useOssTransferStore` (Task 3), `OSSTransferDock` (Task 5), `useOssFileDrop` (Task 7), the additive `onUpload`/`onDownload` props (Task 6), `oss.transfer.*` i18n. The panel already imports `toast` from `sonner` and reads the browser store.

- [ ] **Step 1: Add the failing test** — the P3b-1 test file ALREADY imports `OSSListBuckets`, `OSSBrowserPanel`, `useTabStore`, `useOssBrowserStore`, and `screen`/`fireEvent`/`waitFor`. Add ONLY the one new binder symbol to the existing oss-binder import line — `import { OSSListBuckets, OSSUploadObject } from "../../../../wailsjs/go/oss/OSS";` — do NOT re-import the already-imported symbols (duplicate imports fail lint/tsc). Then add this case inside the `OSSBrowserPanel` describe:

```tsx
it("clicking upload starts an upload into the current prefix", async () => {
  vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
  vi.mocked(OSSUploadObject).mockResolvedValue([] as never);
  render(<OSSBrowserPanel tabId={TAB} />);
  await screen.findByTestId("oss-bucket-b1");
  // put the tab into a selected-bucket state so the breadcrumb renders
  useOssBrowserStore.setState((s) => ({
    tabs: { ...s.tabs, [TAB]: { ...s.tabs[TAB], currentBucket: "b1", currentPrefix: "docs/", listing: { objects: [], prefixes: [], truncated: false, cursor: "" } } },
  }) as never);
  fireEvent.click(await screen.findByTestId("oss-upload"));
  await waitFor(() => expect(OSSUploadObject).toHaveBeenCalledWith(7, "b1", "docs/"));
});
```

(Keep this test resilient: it asserts the binding was invoked with the current bucket/prefix. If the store-shape spread differs, adjust the `setState` to match `ossBrowserStore`'s `OssBrowserTabState`.)

- [ ] **Step 2: Run — fails** — `pnpm test src/components/query/__tests__/OSSBrowserPanel.test.tsx`. Expected: no `oss-upload` testid yet (breadcrumb has no `onUpload` wired).

- [ ] **Step 3: Implement — wire the panel.** In `OSSBrowserPanel.tsx`:
  1. Imports:
  ```tsx
  import { useOssTransferStore } from "@/stores/ossTransferStore";
  import { OSSTransferDock } from "@/components/oss/OSSTransferDock";
  import { useOssFileDrop } from "@/components/oss/useOssFileDrop";
  ```
  2. Inside the component (after the existing browser-store selectors):
  ```tsx
  const transferTab = useOssTransferStore((s) => s.tabs[tabId]);
  const startUpload = useOssTransferStore((s) => s.startUpload);
  const startDownload = useOssTransferStore((s) => s.startDownload);
  const cancelTransfer = useOssTransferStore((s) => s.cancel);
  const clearTransfer = useOssTransferStore((s) => s.clear);
  const clearCompleted = useOssTransferStore((s) => s.clearCompleted);
  const transfers = useMemo(() => Object.values(transferTab?.transfers ?? {}), [transferTab]);

  const contentRef = useRef<HTMLDivElement>(null);
  const isDragOver = useOssFileDrop({
    dropRef: contentRef,
    tabId,
    assetId: assetId ?? 0,
    bucket: state?.currentBucket ?? "",
    prefix: state?.currentPrefix ?? "",
    active: !!assetId && !!state?.currentBucket,
  });

  const onUpload = useCallback(() => {
    if (!assetId || !state?.currentBucket) return;
    void startUpload(tabId, assetId, state.currentBucket, state.currentPrefix).catch(() =>
      toast.error(t("oss.transfer.uploadFailed"))
    );
  }, [assetId, state?.currentBucket, state?.currentPrefix, startUpload, tabId, t]);

  const onDownload = useCallback(
    (key: string) => {
      if (!assetId || !state?.currentBucket) return;
      void startDownload(tabId, assetId, state.currentBucket, key).catch(() =>
        toast.error(t("oss.transfer.downloadFailed"))
      );
    },
    [assetId, state?.currentBucket, startDownload, tabId, t]
  );
  ```
  3. On the content wrapper `<div className="flex min-w-0 flex-1 flex-col">` (the right pane), add the ref, the wails-drop-target style, and `position: relative` so the mask can overlay:
  ```tsx
  <div
    ref={contentRef}
    className="relative flex min-w-0 flex-1 flex-col"
    style={{ "--wails-drop-target": state?.currentBucket ? "drop" : undefined } as React.CSSProperties}
  >
  ```
  (Add `import type React from "react";` if not present, or use the `CSSProperties` type already imported elsewhere in the file.)
  4. Pass `onUpload` to the breadcrumb and `onDownload` to the object list (both already accept the optional props from Task 6):
  ```tsx
  <OSSBreadcrumb bucket={state.currentBucket} prefix={state.currentPrefix} onNavigate={onNavigate} onRefresh={() => void refresh(tabId).catch(fail)} onUpload={onUpload} />
  ...
  <OSSObjectList ... onNavigatePrefix={onNavigate} onToggleSelect={(key) => toggleSelect(tabId, key)} onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)} onDownload={onDownload} />
  ```
  5. Render the drag-over mask inside the content wrapper (when `isDragOver`), and the dock below the object list (when transfers exist). Inside the `state?.currentBucket ? (...)` branch, after `<OSSObjectList .../>`:
  ```tsx
  {transfers.length > 0 && (
    <OSSTransferDock transfers={transfers} onCancel={cancelTransfer} onClear={(id) => clearTransfer(tabId, id)} onClearCompleted={() => clearCompleted(tabId)} />
  )}
  ```
  And, still inside the content wrapper (as a sibling overlay), the mask:
  ```tsx
  {isDragOver && (
    <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-primary/10 text-sm text-primary" data-testid="oss-drop-hint">
      {t("oss.transfer.dropHint")}
    </div>
  )}
  ```
  Ensure `useCallback`, `useMemo`, `useRef` are imported (the panel already imports several React hooks — extend that import).

- [ ] **Step 4: Run — passes** — `pnpm test src/components/query/__tests__/OSSBrowserPanel.test.tsx`, then the FULL suite `pnpm test` (final task — proves i18n parity/coverage with all `oss.transfer.*` literals now referenced and every prior spec still green), then `npx tsc -b`, then `pnpm lint` (`eslint --fix` the panel if needed).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/query/OSSBrowserPanel.tsx frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx
git status --short
git commit -m "✨ 面板接线 OSS 传输 dock/拖放/上传下载与自动刷新"
```

---

## Final verification (after Task 8)

- [ ] From `frontend/`: `pnpm test` (full, all green) · `npx tsc -b` · `pnpm lint` (no new errors in the P3b-2 files), then `git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml` and confirm `git status --short` is clean of lockfile/generated churn.
- [ ] **Observational verification (spec §6 — observe, don't assert; requires a local MinIO OSS asset):** run the app (`make dev`), connect an OSS asset, select a bucket. (1) Click 上传 → pick files → watch the dock rows progress to done and the list auto-refresh. (2) Drag files from the desktop onto the object area → the drop-hint highlights → files upload. (3) Hover an object row → click download → pick a save location → watch the download row progress. (4) Start a large upload and click the row's cancel → confirm it flips to `cancelled` (this is the **upload-cancel context-propagation check** — verify minio `PutObject` actually aborts). Read `logs/opskat.log` for the transfer key-flow lines.

## Spec → task coverage map

- §1.1 bindings / §1.2 progress event / §1.3 file-drop / §1.4 no-sftp-reuse → Tasks 3, 4, 7 (store subscription, coordinator, drop hook).
- §2 decisions (button+drag-drop, single download, defer retry, auto-refresh, status vocab, 5s linger, additive coordinator) → Tasks 3 (vocab, linger, auto-refresh), 4 (coordinator), 6 (upload/download entry), 7 (drag-drop).
- §3.1 store → Task 3. §3.2 dock → Task 5. §3.3 formatBytes/formatSpeed → Task 1. §3.4 coordinator → Task 4. §3.5 overlay (realized as the `useOssFileDrop` hook + panel mask, mirroring `useNativeFileDrop`) → Tasks 7 + 8. §3.6 panel integration → Task 8. §3.7 additive props → Task 6. §3.8 i18n → Task 2.
- §4 data flow → Tasks 3 + 8. §5 error/empty/loading → Tasks 3 (store rethrow + refresh-catch) + 8 (toast wiring). §6 testing incl. deferrals → each task's tests + Final verification.
