# OSS Object Browser — P3b-3 (Detail · Share · Grid) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the OSS object browser with a right-side object **detail pane**, a presigned-URL **share dialog**, and a **list/grid view toggle** with lazy image thumbnails — all frontend, on the existing P3a bindings.

**Architecture:** Extend the per-tab `ossBrowserStore` with `viewMode`/`focusedKey`/`thumbnails` + four actions. Add six new frontend units (`objectContentType` util, `useLazyThumbnail`+`OSSThumbnail`, `OSSObjectDetail`, `OSSPresignDialog`, `OSSObjectGrid`) and additive props on the P3b-1 `OSSObjectList`/`OSSBreadcrumb`. `OSSBrowserPanel` wires it all: swaps list↔grid, opens the detail pane on single-click, hosts the share dialog and a single-object delete confirm.

**Tech Stack:** React 19 + TypeScript, Zustand, `@opskat/ui` (`Dialog`/`Button`/`ScrollArea`/`Checkbox`/`Textarea`/`ConfirmDialog`/`useResizeHandle`), `lucide-react`, `IntersectionObserver`, vitest + happy-dom.

**Base:** P3b-2 head `c88639d4` (spec commit `fb3a95c0` is docs-only on top). Spec: `docs/superpowers/specs/2026-07-09-oss-frontend-p3b-3.md`.

## Global Constraints

- **NO new backend.** Use existing bindings from `../../wailsjs/go/oss/OSS`: `OSSPresignGet(req)`, `OSSPresignPut(req)` (both `Promise<string>`), `OSSStatObject(req)` (unused by P3b-3), `OSSRemoveObject(req)`. Shapes: `PresignRequest { assetId, bucket, key, expirySecs }` (`expirySecs<=0` → server default 1h); `ObjectRequest { assetId, bucket, key }`; `ObjectItem { key, size, lastModified, etag, storageClass, contentType, isPrefix }` (`lastModified` is int64 **seconds** → ×1000 for JS `Date`).
- **`focusedKey` is distinct from `selection`.** Single-click = focus (detail pane); checkbox = multi-select-delete; double-click folder = navigate. These never interfere: the checkbox cell (and the row download button) must `stopPropagation` so they don't also fire the row's focus handler.
- **Grid is browse-only** — no checkbox, no selection bar on tiles. Multi-select-delete stays list-only.
- **Thumbnails: `image/*` only, lazy per-visible, cached per key, one presign per image per session.** Presign failure or `<img>` error → **silent** fallback to the type-icon (NO toast — this is the one sanctioned no-swallow exception; it is a best-effort preview, not a user action). Share/download/delete failures (user-initiated) DO toast.
- **`focusedKey` clears whenever the listing is replaced** (fold `focusedKey: null` into `navigateToPrefix`'s + `selectBucket`'s first patch; `refresh` inherits it via `navigateToPrefix`). View-toggle does NOT clear it.
- **Reuse, don't duplicate:** extend `ossBrowserStore` (its `patch`/`tabCloseHook`), `formatBytes` (`@/lib/formatBytes`), `notifyCopied`/`notifySuccess` (`@/lib/notify`), `startDownload` (`ossTransferStore`, P3b-2), `shouldLoadNextPage` (exported from `OSSObjectList`), `prefixLeafName` (`@/lib/ossPrefixTree`), `useResizeHandle` (`reverse: true` for the right-edge detail pane).
- **Toast convention:** copy → `notifyCopied` (top-center 1s); other success → `notifySuccess`; errors → `toast.error`. Never `toast.success` directly.
- **i18n:** new keys under `oss.detail.*` / `oss.share.*` / `oss.view.*`, en + zh-CN lockstep, idiomatic per language. Reuse existing `oss.browser.{emptyDir,loading,loadingMore,deleteConfirmOne,deleteConfirmTitle,deleteSuccess,deleteFailed,colName,colSize}` and `action.{cancel,confirm}`.
- **ENV TRAPS:** `pnpm test`/`pnpm lint` rewrite `frontend/pnpm-lock.yaml` (and sometimes `frontend/pnpm-workspace.yaml`). Every commit: `git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml`, then `git add` ONLY the task's target files, then `git status --short`. NEVER commit the lockfile/workspace file or anything under `frontend/wailsjs/**` (generated/gitignored).
- **CONCURRENT-WORK HAZARD:** the working tree holds UNCOMMITTED changes from a parallel session — `frontend/src/components/etcd/EtcdClusterBar.tsx`, `EtcdTreePane.tsx`, `frontend/src/components/query/EtcdPanel.tsx`, and etcd keys in both `common.json`, plus an untracked `docs/superpowers/specs/2026-07-09-ai-follow-terminal-design.md`. **NEVER touch, stage, revert, or `git add` any etcd file or any i18n key outside the `oss.*` keys this plan adds.** In a full-suite run, failures in `src/__tests__/EtcdTreePane.test.tsx` / `EtcdPanel.test.tsx` or an etcd-key i18n asymmetry are EXTERNAL and out of scope — record them, do not fix them. Stage only each task's own files.
- **Test env (already set up in `src/__tests__/setup.ts`):** `mockBinderModule` auto-mocks EVERY export of `wailsjs/go/oss/OSS` as `vi.fn().mockResolvedValue(undefined)` (so `OSSPresignGet`/`OSSPresignPut`/`OSSRemoveObject`/`OSSStatObject` are already mocked — drive them with `vi.mocked(...)`, do NOT edit setup.ts). `react-i18next` is mocked with a STABLE `t` returning the key verbatim. `IntersectionObserver` is NOT provided by happy-dom — component tests that use it must install a mock (shown in Task 4).
- **Per-task gates (from `frontend/`):** `pnpm test <task test path>` (RED then GREEN); `npx tsc -b` clean; `pnpm lint` adds no NEW errors in the task's files (a `react-refresh/only-export-components` warning on a file co-exporting a helper + component is acceptable; run `eslint --fix` on ONLY the task's files for prettier line-wrap).

---

## File Structure

- **Create** `frontend/src/lib/objectContentType.ts` — `isImage` + `typeIcon` pure helpers. Test: `frontend/src/lib/__tests__/objectContentType.test.ts`.
- **Create** `frontend/src/components/oss/useLazyThumbnail.ts` — IntersectionObserver-once hook. Test: `frontend/src/components/oss/__tests__/useLazyThumbnail.test.tsx`.
- **Create** `frontend/src/components/oss/OSSThumbnail.tsx` — lazy `<img>`/icon. Test: `frontend/src/components/oss/__tests__/OSSThumbnail.test.tsx`.
- **Create** `frontend/src/components/oss/OSSObjectDetail.tsx` — right detail pane. Test: `.../__tests__/OSSObjectDetail.test.tsx`.
- **Create** `frontend/src/components/oss/OSSPresignDialog.tsx` — share modal. Test: `.../__tests__/OSSPresignDialog.test.tsx`.
- **Create** `frontend/src/components/oss/OSSObjectGrid.tsx` — grid tiles. Test: `.../__tests__/OSSObjectGrid.test.tsx`.
- **Modify** `frontend/src/stores/ossBrowserStore.ts` (+ append tests to `ossBrowserStore.test.ts`) — state + 4 actions + focus-clear.
- **Modify** `frontend/src/components/oss/OSSObjectList.tsx` (+ its test) — `focusedKey`/`onFocusObject` + checkbox `stopPropagation`.
- **Modify** `frontend/src/components/oss/OSSBreadcrumb.tsx` (+ its test) — `viewMode`/`onViewModeChange` toggle.
- **Modify** `frontend/src/components/query/OSSBrowserPanel.tsx` (+ its test) — wiring.
- **Modify** `frontend/src/i18n/locales/en/common.json` + `zh-CN/common.json` — `oss.detail.*`/`oss.share.*`/`oss.view.*`.

---

### Task 1: `objectContentType.ts` — `isImage` + `typeIcon`

**Files:**
- Create: `frontend/src/lib/objectContentType.ts`
- Create: `frontend/src/lib/__tests__/objectContentType.test.ts`

**Interfaces:**
- Produces: `isImage(contentType: string, key?: string): boolean`; `typeIcon(contentType: string, key: string): LucideIcon` (where `LucideIcon = typeof import("lucide-react").File`).
- Consumes: `lucide-react` icons only (pure otherwise).

- [ ] **Step 1: Write the failing test** — `frontend/src/lib/__tests__/objectContentType.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { FileImage, FileVideo, FileJson, File as FileIcon } from "lucide-react";
import { isImage, typeIcon } from "../objectContentType";

describe("isImage", () => {
  it("detects image content-types", () => {
    expect(isImage("image/png")).toBe(true);
    expect(isImage("image/jpeg")).toBe(true);
    expect(isImage("application/octet-stream")).toBe(false);
    expect(isImage("")).toBe(false);
  });
  it("falls back to the key extension when content-type is blank", () => {
    expect(isImage("", "photos/a.PNG")).toBe(true);
    expect(isImage("", "docs/a.txt")).toBe(false);
    expect(isImage("application/octet-stream", "a.webp")).toBe(true);
  });
});

describe("typeIcon", () => {
  it("maps families and defaults to a generic file icon", () => {
    expect(typeIcon("image/png", "a.png")).toBe(FileImage);
    expect(typeIcon("video/mp4", "a.mp4")).toBe(FileVideo);
    expect(typeIcon("application/json", "a.json")).toBe(FileJson);
    expect(typeIcon("", "a.json")).toBe(FileJson);
    expect(typeIcon("application/octet-stream", "a.bin")).toBe(FileIcon);
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/lib/__tests__/objectContentType.test.ts`. Expected: `Failed to resolve import "../objectContentType"`.

- [ ] **Step 3: Implement** — `frontend/src/lib/objectContentType.ts`:

```ts
import { FileImage, FileVideo, FileAudio, FileJson, FileText, FileArchive, File as FileIcon } from "lucide-react";

export type LucideIcon = typeof FileIcon;

const IMAGE_EXT = ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "avif"];

function ext(key: string): string {
  const i = key.lastIndexOf(".");
  return i >= 0 ? key.slice(i + 1).toLowerCase() : "";
}

/** image/* by content-type, or an image extension when content-type is blank/generic. */
export function isImage(contentType: string, key = ""): boolean {
  if (contentType.startsWith("image/")) return true;
  return IMAGE_EXT.includes(ext(key));
}

/** Family → lucide icon; content-type first, extension fallback, generic File default. */
export function typeIcon(contentType: string, key: string): LucideIcon {
  if (isImage(contentType, key)) return FileImage;
  if (contentType.startsWith("video/") || ["mp4", "mov", "webm", "mkv", "avi"].includes(ext(key))) return FileVideo;
  if (contentType.startsWith("audio/") || ["mp3", "wav", "flac", "ogg", "m4a"].includes(ext(key))) return FileAudio;
  if (contentType.includes("json") || ext(key) === "json") return FileJson;
  if (["zip", "gz", "tar", "rar", "7z"].includes(ext(key))) return FileArchive;
  if (contentType.startsWith("text/") || ["txt", "md", "log", "csv", "yaml", "yml", "xml"].includes(ext(key)))
    return FileText;
  return FileIcon;
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/lib/__tests__/objectContentType.test.ts`, then `npx tsc -b`, then `pnpm lint` (fix only these files).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/lib/objectContentType.ts frontend/src/lib/__tests__/objectContentType.test.ts
git status --short
git commit -m "✨ OSS 对象内容类型工具（isImage/typeIcon）"
```

---

### Task 2: i18n `oss.detail.*` / `oss.share.*` / `oss.view.*`

**Files:**
- Modify: `frontend/src/i18n/locales/en/common.json`
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`

**Interfaces:**
- Produces: the three key namespaces consumed by Tasks 5–9.

Add three objects inside the existing `"oss"` object (siblings of `"browser"`/`"transfer"`) in BOTH locale files, with identical key sets.

- [ ] **Step 1: Add to `en/common.json`** (inside `"oss": { ... }`):

```json
"view": {
  "list": "List",
  "grid": "Grid"
},
"detail": {
  "copyKey": "Copy key",
  "share": "Share link",
  "download": "Download",
  "delete": "Delete",
  "close": "Close",
  "noPreview": "No preview",
  "keyLabel": "Key",
  "size": "Size",
  "type": "Type",
  "etag": "ETag",
  "storageClass": "Storage class",
  "lastModified": "Last modified"
},
"share": {
  "title": "Generate presigned URL",
  "methodGet": "GET · download",
  "methodPut": "PUT · upload",
  "expiry15m": "15 min",
  "expiry1h": "1 hour",
  "expiry24h": "24 hours",
  "expiry7d": "7 days",
  "urlLabel": "Presigned URL",
  "generate": "Generate",
  "regenerate": "Regenerate",
  "close": "Close",
  "copyAndClose": "Copy & close",
  "warning": "Anyone with this link can access the object until it expires.",
  "generateFailed": "Failed to generate URL",
  "copied": "Link copied"
}
```

- [ ] **Step 2: Add to `zh-CN/common.json`** (same location, same keys, idiomatic Chinese):

```json
"view": {
  "list": "列表",
  "grid": "网格"
},
"detail": {
  "copyKey": "复制键名",
  "share": "分享链接",
  "download": "下载",
  "delete": "删除",
  "close": "关闭",
  "noPreview": "无预览",
  "keyLabel": "键名",
  "size": "大小",
  "type": "类型",
  "etag": "ETag",
  "storageClass": "存储类型",
  "lastModified": "最后修改"
},
"share": {
  "title": "生成预签名 URL",
  "methodGet": "GET · 下载",
  "methodPut": "PUT · 上传",
  "expiry15m": "15 分钟",
  "expiry1h": "1 小时",
  "expiry24h": "24 小时",
  "expiry7d": "7 天",
  "urlLabel": "预签名 URL",
  "generate": "生成",
  "regenerate": "重新生成",
  "close": "关闭",
  "copyAndClose": "复制并关闭",
  "warning": "在过期前，任何持有此链接的人都能访问该对象。",
  "generateFailed": "生成 URL 失败",
  "copied": "链接已复制"
}
```

- [ ] **Step 3: Run — passes** — `pnpm test src/__tests__/i18n.test.ts`. Expected: GREEN (en⇔zh parity; unused-yet keys allowed). If it flags a missing/asymmetric key or invalid JSON, fix it. **If it reports an asymmetry in an `etcd.*` key, that is the concurrent session's work — do NOT touch it; re-run to confirm your `oss.*` additions are symmetric and move on.**

- [ ] **Step 4: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
git status --short
git commit -m "🌐 新增 OSS 详情/分享/视图文案 oss.detail/share/view.*"
```

---

### Task 3: `ossBrowserStore` extensions — `viewMode`/`focusedKey`/`thumbnails` + 4 actions

**Files:**
- Modify: `frontend/src/stores/ossBrowserStore.ts`
- Modify: `frontend/src/stores/ossBrowserStore.test.ts` (append)

**Interfaces:**
- Consumes: `OSSPresignGet`, `OSSRemoveObject` from `../../wailsjs/go/oss/OSS`.
- Produces (on `OssBrowserTabState`): `viewMode: "list"|"grid"`, `focusedKey: string|null`, `thumbnails: Record<string,string>`. On the store: `setViewMode(tabId, mode)`, `focusObject(tabId, key|null)`, `ensureThumbnail(tabId, key): Promise<void>`, `deleteObject(tabId, key): Promise<void>`.

- [ ] **Step 1: Write the failing tests** — append to `frontend/src/stores/ossBrowserStore.test.ts` (reuse the file's existing imports/`beforeEach`; it already imports `useOssBrowserStore` and resets state. Add `OSSPresignGet`, `OSSRemoveObject` to the existing `wailsjs/go/oss/OSS` import line if not already present, and reset them in your test bodies):

```ts
import { OSSPresignGet, OSSRemoveObject, OSSListObjects } from "../../wailsjs/go/oss/OSS";

describe("ossBrowserStore P3b-3 additions", () => {
  const TAB = "query-detail";
  function seed(over: Partial<import("./ossBrowserStore").OssBrowserTabState> = {}) {
    useOssBrowserStore.setState({
      tabs: {
        [TAB]: {
          assetId: 7, buckets: [], currentBucket: "b", currentPrefix: "docs/",
          tree: {}, expanded: new Set(),
          listing: { objects: [{ key: "docs/a.txt", size: 1, lastModified: 0, etag: "", storageClass: "", contentType: "", isPrefix: false }] as never, prefixes: [], cursor: "", truncated: false },
          selection: new Set(), loading: { buckets: false, listing: false, page: false }, error: null,
          viewMode: "list", focusedKey: null, thumbnails: {},
          ...over,
        } as never,
      },
    } as never);
  }

  it("setViewMode flips the tab's view mode", () => {
    seed();
    useOssBrowserStore.getState().setViewMode(TAB, "grid");
    expect(useOssBrowserStore.getState().tabs[TAB].viewMode).toBe("grid");
  });

  it("focusObject sets and clears the focused key", () => {
    seed();
    useOssBrowserStore.getState().focusObject(TAB, "docs/a.txt");
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBe("docs/a.txt");
    useOssBrowserStore.getState().focusObject(TAB, null);
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBeNull();
  });

  it("ensureThumbnail presigns once and caches the URL", async () => {
    seed();
    vi.mocked(OSSPresignGet).mockResolvedValue("https://signed/a" as never);
    await useOssBrowserStore.getState().ensureThumbnail(TAB, "docs/a.txt");
    expect(useOssBrowserStore.getState().tabs[TAB].thumbnails["docs/a.txt"]).toBe("https://signed/a");
    await useOssBrowserStore.getState().ensureThumbnail(TAB, "docs/a.txt"); // cached → no second call
    expect(OSSPresignGet).toHaveBeenCalledTimes(1);
  });

  it("ensureThumbnail leaves no cache entry on presign failure (silent)", async () => {
    seed();
    vi.mocked(OSSPresignGet).mockRejectedValue(new Error("boom") as never);
    await useOssBrowserStore.getState().ensureThumbnail(TAB, "docs/a.txt");
    expect(useOssBrowserStore.getState().tabs[TAB].thumbnails["docs/a.txt"]).toBeUndefined();
  });

  it("deleteObject removes one object, refreshes, and clears focus", async () => {
    seed({ focusedKey: "docs/a.txt" });
    vi.mocked(OSSRemoveObject).mockResolvedValue(undefined as never);
    const refresh = vi.spyOn(useOssBrowserStore.getState(), "refresh").mockResolvedValue(undefined);
    await useOssBrowserStore.getState().deleteObject(TAB, "docs/a.txt");
    expect(OSSRemoveObject).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt" });
    expect(refresh).toHaveBeenCalledWith(TAB);
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBeNull();
    refresh.mockRestore();
  });

  it("navigateToPrefix clears focusedKey", async () => {
    seed({ focusedKey: "docs/a.txt" });
    vi.mocked(OSSListObjects).mockResolvedValue({ objects: [], prefixes: [], nextContinuationToken: "", isTruncated: false } as never);
    await useOssBrowserStore.getState().navigateToPrefix(TAB, "other/");
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBeNull();
  });
});
```

(If the existing `beforeEach` in this file resets `tabs` to `{}`, the `seed()` helper above re-seeds per test — keep it. If the existing file lacks `vi` in scope, add it to the top `vitest` import.)

- [ ] **Step 2: Run — fails** — `pnpm test src/stores/ossBrowserStore.test.ts`. Expected: type/name errors — `setViewMode`/`focusObject`/`ensureThumbnail`/`deleteObject` don't exist, `viewMode`/`focusedKey`/`thumbnails` missing on the state.

- [ ] **Step 3: Implement** — in `frontend/src/stores/ossBrowserStore.ts`:

1. Extend the import line: `import { OSSListBuckets, OSSListObjects, OSSRemoveObject, OSSRemoveObjects, OSSPresignGet } from "../../wailsjs/go/oss/OSS";`
2. Add three fields to `OssBrowserTabState` (after `error`):
```ts
  viewMode: "list" | "grid";
  focusedKey: string | null;
  thumbnails: Record<string, string>;
```
3. Add four actions to the `OssBrowserState` interface (after `refresh`):
```ts
  setViewMode: (tabId: string, mode: "list" | "grid") => void;
  focusObject: (tabId: string, key: string | null) => void;
  ensureThumbnail: (tabId: string, key: string) => Promise<void>;
  deleteObject: (tabId: string, key: string) => Promise<void>;
```
4. Initialize the three fields in `emptyTabState` (after `error: null,`):
```ts
    viewMode: "list",
    focusedKey: null,
    thumbnails: {},
```
5. Add a module-level in-flight guard ABOVE `export const useOssBrowserStore` (so concurrent `ensureThumbnail` calls don't double-presign, without a re-render-causing store field):
```ts
const thumbInFlight = new Set<string>(); // `${tabId} ${key}`
```
6. Fold `focusedKey: null` into the first patch of `navigateToPrefix` (the one that sets `currentPrefix`/`selection`) and into `selectBucket`'s patch:
   - In `navigateToPrefix`, the first `patch(tabId, (t) => ({ ...t, currentPrefix: prefix, selection: new Set(), ... }))` → add `focusedKey: null,`.
   - In `selectBucket`, the `patch(tabId, (t) => ({ ...t, currentBucket: bucket, ... selection: new Set() }))` → add `focusedKey: null,`.
   (`refresh` calls `navigateToPrefix`, so it inherits the clear.)
7. Add the four action implementations to the returned object (after `refresh`):
```ts
    setViewMode: (tabId, mode) => patch(tabId, (t) => ({ ...t, viewMode: mode })),

    focusObject: (tabId, key) => patch(tabId, (t) => ({ ...t, focusedKey: key })),

    ensureThumbnail: async (tabId, key) => {
      const t0 = get().tabs[tabId];
      if (!t0 || t0.thumbnails[key]) return; // 已缓存
      const flightKey = `${tabId} ${key}`;
      if (thumbInFlight.has(flightKey)) return; // 生成中
      thumbInFlight.add(flightKey);
      try {
        const url = await OSSPresignGet({ assetId: t0.assetId, bucket: t0.currentBucket, key, expirySecs: 0 });
        if (url) patch(tabId, (t) => ({ ...t, thumbnails: { ...t.thumbnails, [key]: url } }));
      } catch {
        // 缩略图为尽力而为的预览，presign 失败静默回退到类型图标（唯一豁免的吞错点，见 spec §2.9）
      } finally {
        thumbInFlight.delete(flightKey);
      }
    },

    deleteObject: async (tabId, key) => {
      const t0 = get().tabs[tabId];
      if (!t0) return;
      try {
        await OSSRemoveObject({ assetId: t0.assetId, bucket: t0.currentBucket, key });
      } catch (err) {
        patch(tabId, (t) => ({ ...t, error: String(err) }));
        throw err;
      }
      patch(tabId, (t) => (t.focusedKey === key ? { ...t, focusedKey: null } : t));
      await get().refresh(tabId);
    },
```

- [ ] **Step 4: Run — passes** — `pnpm test src/stores/ossBrowserStore.test.ts`, then `npx tsc -b`, then `pnpm lint` (fix only this file).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/stores/ossBrowserStore.ts frontend/src/stores/ossBrowserStore.test.ts
git status --short
git commit -m "✨ OSS 浏览 store 扩展视图/焦点/缩略图 + 单对象删除"
```

---

### Task 4: `useLazyThumbnail` + `OSSThumbnail`

**Files:**
- Create: `frontend/src/components/oss/useLazyThumbnail.ts`
- Create: `frontend/src/components/oss/OSSThumbnail.tsx`
- Create: `frontend/src/components/oss/__tests__/useLazyThumbnail.test.tsx`
- Create: `frontend/src/components/oss/__tests__/OSSThumbnail.test.tsx`

**Interfaces:**
- Consumes: `isImage`/`typeIcon` (Task 1).
- Produces: `useLazyThumbnail(ref: RefObject<HTMLElement | null>, enabled: boolean, onEnter: () => void): void`; `OSSThumbnail({ objectKey, contentType, url, onEnsure, className })`.

- [ ] **Step 1: Write the failing tests.**

`frontend/src/components/oss/__tests__/useLazyThumbnail.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { useLazyThumbnail } from "../useLazyThumbnail";

let observeCb: IntersectionObserverCallback | null = null;
const observe = vi.fn();
const disconnect = vi.fn();

beforeEach(() => {
  observeCb = null;
  observe.mockClear();
  disconnect.mockClear();
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(cb: IntersectionObserverCallback) {
        observeCb = cb;
      }
      observe = observe;
      disconnect = disconnect;
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as never
  );
});
afterEach(() => vi.unstubAllGlobals());

function Harness({ enabled, onEnter }: { enabled: boolean; onEnter: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useLazyThumbnail(ref, enabled, onEnter);
  return <div ref={ref} />;
}

describe("useLazyThumbnail", () => {
  it("fires onEnter once when the element intersects, then disconnects", () => {
    const onEnter = vi.fn();
    render(<Harness enabled onEnter={onEnter} />);
    expect(observe).toHaveBeenCalledTimes(1);
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(onEnter).toHaveBeenCalledTimes(1);
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(onEnter).toHaveBeenCalledTimes(1); // disconnected after first
  });

  it("does not observe when disabled", () => {
    render(<Harness enabled={false} onEnter={vi.fn()} />);
    expect(observe).not.toHaveBeenCalled();
  });
});
```

`frontend/src/components/oss/__tests__/OSSThumbnail.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { OSSThumbnail } from "../OSSThumbnail";

let observeCb: IntersectionObserverCallback | null = null;
beforeEach(() => {
  observeCb = null;
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(cb: IntersectionObserverCallback) {
        observeCb = cb;
      }
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as never
  );
});
afterEach(() => vi.unstubAllGlobals());

describe("OSSThumbnail", () => {
  it("calls onEnsure when an image scrolls into view and renders the img once url is set", () => {
    const onEnsure = vi.fn();
    const { rerender } = render(
      <OSSThumbnail objectKey="a.png" contentType="image/png" onEnsure={onEnsure} />
    );
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(onEnsure).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("oss-thumb-img")).toBeNull(); // no url yet → placeholder
    rerender(<OSSThumbnail objectKey="a.png" contentType="image/png" url="https://x/a" onEnsure={onEnsure} />);
    expect(screen.getByTestId("oss-thumb-img")).toHaveAttribute("src", "https://x/a");
  });

  it("renders a type icon (no img, no ensure) for a non-image", () => {
    const onEnsure = vi.fn();
    render(<OSSThumbnail objectKey="a.json" contentType="application/json" onEnsure={onEnsure} />);
    expect(screen.queryByTestId("oss-thumb-img")).toBeNull();
    expect(screen.getByTestId("oss-thumb-icon")).toBeInTheDocument();
    expect(onEnsure).not.toHaveBeenCalled();
  });

  it("falls back to the icon when the image errors", () => {
    render(<OSSThumbnail objectKey="a.png" contentType="image/png" url="https://x/broken" onEnsure={vi.fn()} />);
    screen.getByTestId("oss-thumb-img").dispatchEvent(new Event("error"));
    expect(screen.getByTestId("oss-thumb-icon")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/useLazyThumbnail.test.tsx src/components/oss/__tests__/OSSThumbnail.test.tsx`. Expected: unresolved imports.

- [ ] **Step 3a: Implement the hook** — `frontend/src/components/oss/useLazyThumbnail.ts`:

```ts
import { useEffect, type RefObject } from "react";

/** 元素首次进入视口时触发 onEnter 一次然后断开观察。enabled=false 时不观察。 */
export function useLazyThumbnail(ref: RefObject<HTMLElement | null>, enabled: boolean, onEnter: () => void): void {
  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        onEnter();
        observer.disconnect();
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, enabled, onEnter]);
}
```

- [ ] **Step 3b: Implement the component** — `frontend/src/components/oss/OSSThumbnail.tsx`:

```tsx
import { useEffect, useRef, useState } from "react";
import { isImage, typeIcon } from "@/lib/objectContentType";
import { useLazyThumbnail } from "./useLazyThumbnail";

export interface OSSThumbnailProps {
  objectKey: string;
  contentType: string;
  url?: string;
  onEnsure: () => void;
  className?: string;
}

export function OSSThumbnail({ objectKey, contentType, url, onEnsure, className }: OSSThumbnailProps) {
  const ref = useRef<HTMLDivElement>(null);
  const image = isImage(contentType, objectKey);
  const [errored, setErrored] = useState(false);
  useEffect(() => setErrored(false), [url]);
  useLazyThumbnail(ref, image && !errored, onEnsure);

  const Icon = typeIcon(contentType, objectKey);
  const showImg = image && !errored && !!url;

  return (
    <div ref={ref} className={className} data-testid="oss-thumb">
      {showImg ? (
        <img
          src={url}
          alt=""
          className="size-full object-cover"
          data-testid="oss-thumb-img"
          onError={() => setErrored(true)}
        />
      ) : (
        <div className="flex size-full items-center justify-center text-muted-foreground" data-testid="oss-thumb-icon">
          <Icon className="size-6" />
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/useLazyThumbnail.test.tsx src/components/oss/__tests__/OSSThumbnail.test.tsx`, then `npx tsc -b`, then `pnpm lint` (fix only these files).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/useLazyThumbnail.ts frontend/src/components/oss/OSSThumbnail.tsx frontend/src/components/oss/__tests__/useLazyThumbnail.test.tsx frontend/src/components/oss/__tests__/OSSThumbnail.test.tsx
git status --short
git commit -m "✨ OSS 懒加载缩略图 hook 与组件（IntersectionObserver + 图标回退）"
```

---

### Task 5: `OSSObjectDetail` — right detail pane

**Files:**
- Create: `frontend/src/components/oss/OSSObjectDetail.tsx`
- Create: `frontend/src/components/oss/__tests__/OSSObjectDetail.test.tsx`

**Interfaces:**
- Consumes: `oss_svc.ObjectItem` type (`../../../wailsjs/go/models`), `OSSThumbnail` (Task 4), `formatBytes` (`@/lib/formatBytes`), `notifyCopied` (`@/lib/notify`), `@opskat/ui` `Button`, `prefixLeafName` (`@/lib/ossPrefixTree`).
- Produces: `OSSObjectDetail({ object, thumbnailUrl, onEnsureThumbnail, onShare, onDownload, onDelete, onClose })`.

- [ ] **Step 1: Write the failing test** — `frontend/src/components/oss/__tests__/OSSObjectDetail.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSObjectDetail } from "../OSSObjectDetail";
import type { oss_svc } from "../../../../wailsjs/go/models";

beforeEach(() => {
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as never
  );
});
afterEach(() => vi.unstubAllGlobals());

function obj(over: Partial<oss_svc.ObjectItem> = {}): oss_svc.ObjectItem {
  return { key: "docs/report.pdf", size: 2048, lastModified: 0, etag: "e1", storageClass: "STANDARD", contentType: "application/pdf", isPrefix: false, ...over } as oss_svc.ObjectItem;
}

describe("OSSObjectDetail", () => {
  it("renders metadata from the object and fires action callbacks", () => {
    const onShare = vi.fn(), onDownload = vi.fn(), onDelete = vi.fn(), onClose = vi.fn();
    render(
      <OSSObjectDetail object={obj()} onEnsureThumbnail={vi.fn()} onShare={onShare} onDownload={onDownload} onDelete={onDelete} onClose={onClose} />
    );
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("STANDARD")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("oss-detail-share"));
    fireEvent.click(screen.getByTestId("oss-detail-download"));
    fireEvent.click(screen.getByTestId("oss-detail-delete"));
    fireEvent.click(screen.getByTestId("oss-detail-close"));
    expect(onShare).toHaveBeenCalledTimes(1);
    expect(onDownload).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows an icon thumbnail for a non-image and an img for an image with a url", () => {
    const { rerender } = render(<OSSObjectDetail object={obj()} onEnsureThumbnail={vi.fn()} onShare={vi.fn()} onDownload={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByTestId("oss-thumb-icon")).toBeInTheDocument();
    rerender(<OSSObjectDetail object={obj({ key: "img/a.png", contentType: "image/png" })} thumbnailUrl="https://x/a" onEnsureThumbnail={vi.fn()} onShare={vi.fn()} onDownload={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByTestId("oss-thumb-img")).toHaveAttribute("src", "https://x/a");
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/OSSObjectDetail.test.tsx`. Expected: unresolved import `../OSSObjectDetail`.

- [ ] **Step 3: Implement** — `frontend/src/components/oss/OSSObjectDetail.tsx`:

```tsx
import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { Copy, Share2, Download, Trash2, X } from "lucide-react";
import type { oss_svc } from "../../../wailsjs/go/models";
import { formatBytes } from "@/lib/formatBytes";
import { notifyCopied } from "@/lib/notify";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { OSSThumbnail } from "./OSSThumbnail";

export interface OSSObjectDetailProps {
  object: oss_svc.ObjectItem;
  thumbnailUrl?: string;
  onEnsureThumbnail: () => void;
  onShare: () => void;
  onDownload: () => void;
  onDelete: () => void;
  onClose: () => void;
}

export function OSSObjectDetail({
  object,
  thumbnailUrl,
  onEnsureThumbnail,
  onShare,
  onDownload,
  onDelete,
  onClose,
}: OSSObjectDetailProps) {
  const { t } = useTranslation();
  const rows: [string, string][] = [
    [t("oss.detail.size"), formatBytes(object.size)],
    [t("oss.detail.type"), object.contentType || "—"],
    [t("oss.detail.storageClass"), object.storageClass || "—"],
    [t("oss.detail.etag"), object.etag || "—"],
    [t("oss.detail.lastModified"), object.lastModified ? new Date(object.lastModified * 1000).toLocaleString() : "—"],
  ];
  const copyKey = () => void navigator.clipboard?.writeText(object.key).then(() => notifyCopied(t("oss.detail.copyKey")));

  return (
    <div className="flex h-full flex-col text-xs" data-testid="oss-object-detail">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <span className="min-w-0 flex-1 truncate font-medium" title={object.key}>
          {prefixLeafName(object.key)}
        </span>
        <button type="button" onClick={onClose} title={t("oss.detail.close")} data-testid="oss-detail-close">
          <X className="size-3.5 text-muted-foreground" />
        </button>
      </div>

      <div className="aspect-video shrink-0 overflow-hidden border-b bg-muted/20">
        <OSSThumbnail
          objectKey={object.key}
          contentType={object.contentType}
          url={thumbnailUrl}
          onEnsure={onEnsureThumbnail}
          className="size-full"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-3 py-2">
        <div className="mb-2 flex items-center gap-1">
          <span className="min-w-0 flex-1 break-all font-mono text-muted-foreground">{object.key}</span>
          <button type="button" onClick={copyKey} title={t("oss.detail.copyKey")} data-testid="oss-detail-copy-key">
            <Copy className="size-3 text-muted-foreground" />
          </button>
        </div>
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          {rows.map(([label, value]) => (
            <div key={label} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="break-all text-right">{value}</dd>
            </div>
          ))}
        </dl>
      </div>

      <div className="flex flex-col gap-1.5 border-t p-3">
        <Button size="sm" onClick={onShare} data-testid="oss-detail-share">
          <Share2 className="size-3" /> {t("oss.detail.share")}
        </Button>
        <div className="flex gap-1.5">
          <Button size="sm" variant="outline" className="flex-1" onClick={onDownload} data-testid="oss-detail-download">
            <Download className="size-3" /> {t("oss.detail.download")}
          </Button>
          <Button size="sm" variant="destructive" className="flex-1" onClick={onDelete} data-testid="oss-detail-delete">
            <Trash2 className="size-3" /> {t("oss.detail.delete")}
          </Button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/OSSObjectDetail.test.tsx`, then `npx tsc -b`, then `pnpm lint` (fix only this file).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/OSSObjectDetail.tsx frontend/src/components/oss/__tests__/OSSObjectDetail.test.tsx
git status --short
git commit -m "✨ OSS 对象详情面板"
```

---

### Task 6: `OSSPresignDialog` — share modal (GET/PUT + expiry)

**Files:**
- Create: `frontend/src/components/oss/OSSPresignDialog.tsx`
- Create: `frontend/src/components/oss/__tests__/OSSPresignDialog.test.tsx`

**Interfaces:**
- Consumes: `OSSPresignGet`/`OSSPresignPut` (`../../../wailsjs/go/oss/OSS`), `@opskat/ui` `Dialog`/`DialogContent`/`DialogHeader`/`DialogTitle`/`DialogFooter`/`Button`/`Textarea`, `notifyCopied` (`@/lib/notify`), `toast` (`sonner`).
- Produces: `OSSPresignDialog({ open, onOpenChange, assetId, bucket, objectKey })`.

- [ ] **Step 1: Write the failing test** — `frontend/src/components/oss/__tests__/OSSPresignDialog.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { OSSPresignDialog } from "../OSSPresignDialog";
import { OSSPresignGet, OSSPresignPut } from "../../../../wailsjs/go/oss/OSS";

beforeEach(() => {
  vi.mocked(OSSPresignGet).mockReset().mockResolvedValue("https://signed/get" as never);
  vi.mocked(OSSPresignPut).mockReset().mockResolvedValue("https://signed/put" as never);
});

function open() {
  render(<OSSPresignDialog open onOpenChange={vi.fn()} assetId={7} bucket="b" objectKey="docs/a.txt" />);
}

describe("OSSPresignDialog", () => {
  it("generates a GET url with the selected expiry", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-expiry-86400"));
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await waitFor(() =>
      expect(OSSPresignGet).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 86400 })
    );
    expect((await screen.findByTestId<HTMLTextAreaElement>("oss-share-url")).value).toBe("https://signed/get");
  });

  it("uses OSSPresignPut when the PUT method is selected", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-method-put"));
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await waitFor(() =>
      expect(OSSPresignPut).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt", expirySecs: 3600 })
    );
  });

  it("clears a stale url when the method changes", async () => {
    open();
    fireEvent.click(screen.getByTestId("oss-share-generate"));
    await screen.findByDisplayValue("https://signed/get");
    fireEvent.click(screen.getByTestId("oss-share-method-put"));
    expect(screen.getByTestId<HTMLTextAreaElement>("oss-share-url").value).toBe("");
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/OSSPresignDialog.test.tsx`. Expected: unresolved import `../OSSPresignDialog`.

- [ ] **Step 3: Implement** — `frontend/src/components/oss/OSSPresignDialog.tsx`:

```tsx
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, Button, Textarea } from "@opskat/ui";
import { OSSPresignGet, OSSPresignPut } from "../../../wailsjs/go/oss/OSS";
import { notifyCopied } from "@/lib/notify";

export interface OSSPresignDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  bucket: string;
  objectKey: string;
}

type Method = "get" | "put";
const EXPIRIES: { secs: number; key: string }[] = [
  { secs: 900, key: "oss.share.expiry15m" },
  { secs: 3600, key: "oss.share.expiry1h" },
  { secs: 86400, key: "oss.share.expiry24h" },
  { secs: 604800, key: "oss.share.expiry7d" },
];

export function OSSPresignDialog({ open, onOpenChange, assetId, bucket, objectKey }: OSSPresignDialogProps) {
  const { t } = useTranslation();
  const [method, setMethod] = useState<Method>("get");
  const [expirySecs, setExpirySecs] = useState(3600);
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(false);

  // 每次打开重置；改方法/有效期作废旧 URL（强制重新生成）。
  useEffect(() => {
    if (open) {
      setMethod("get");
      setExpirySecs(3600);
      setUrl("");
      setLoading(false);
    }
  }, [open]);

  const pickMethod = (m: Method) => {
    setMethod(m);
    setUrl("");
  };
  const pickExpiry = (secs: number) => {
    setExpirySecs(secs);
    setUrl("");
  };

  const generate = async () => {
    setLoading(true);
    try {
      const req = { assetId, bucket, key: objectKey, expirySecs };
      const u = method === "get" ? await OSSPresignGet(req) : await OSSPresignPut(req);
      setUrl(u);
    } catch (err) {
      toast.error(`${t("oss.share.generateFailed")}: ${String(err)}`);
    } finally {
      setLoading(false);
    }
  };

  const copy = () => void navigator.clipboard?.writeText(url).then(() => notifyCopied(t("oss.share.copied")));
  const copyAndClose = () => {
    copy();
    onOpenChange(false);
  };

  const seg = (active: boolean) =>
    `flex-1 rounded px-2 py-1 ${active ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground"}`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("oss.share.title")}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-3 text-xs">
          <div className="truncate font-mono text-muted-foreground" title={objectKey}>
            {objectKey}
          </div>

          <div className="flex gap-1">
            <button type="button" className={seg(method === "get")} onClick={() => pickMethod("get")} data-testid="oss-share-method-get">
              {t("oss.share.methodGet")}
            </button>
            <button type="button" className={seg(method === "put")} onClick={() => pickMethod("put")} data-testid="oss-share-method-put">
              {t("oss.share.methodPut")}
            </button>
          </div>

          <div className="flex gap-1">
            {EXPIRIES.map((e) => (
              <button
                key={e.secs}
                type="button"
                className={seg(expirySecs === e.secs)}
                onClick={() => pickExpiry(e.secs)}
                data-testid={`oss-share-expiry-${e.secs}`}
              >
                {t(e.key)}
              </button>
            ))}
          </div>

          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground">{t("oss.share.urlLabel")}</span>
            <Textarea readOnly value={url} rows={3} className="font-mono" data-testid="oss-share-url" />
          </div>

          <p className="text-warning">{t("oss.share.warning")}</p>
        </div>

        <DialogFooter className="flex-row justify-between gap-2">
          <Button size="sm" variant="outline" onClick={() => void generate()} disabled={loading} data-testid="oss-share-generate">
            {url ? t("oss.share.regenerate") : t("oss.share.generate")}
          </Button>
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("oss.share.close")}
            </Button>
            <Button size="sm" onClick={copyAndClose} disabled={!url} data-testid="oss-share-copy-close">
              {t("oss.share.copyAndClose")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/OSSPresignDialog.test.tsx`, then `npx tsc -b`, then `pnpm lint` (fix only this file). (If a Radix Dialog portal warning appears in test output, it is benign; the test asserts on portaled content via `screen` which queries `document.body`.)

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/OSSPresignDialog.tsx frontend/src/components/oss/__tests__/OSSPresignDialog.test.tsx
git status --short
git commit -m "✨ OSS 预签名分享对话框（GET/PUT + 有效期）"
```

---

### Task 7: `OSSObjectGrid` — browse-only tile view

**Files:**
- Create: `frontend/src/components/oss/OSSObjectGrid.tsx`
- Create: `frontend/src/components/oss/__tests__/OSSObjectGrid.test.tsx`

**Interfaces:**
- Consumes: `oss_svc.ObjectItem`, `OSSThumbnail` (Task 4), `formatBytes` (`@/lib/formatBytes`), `prefixLeafName` (`@/lib/ossPrefixTree`), `shouldLoadNextPage` (re-exported from `./OSSObjectList`), `lucide-react` `Folder`.
- Produces: `OSSObjectGrid({ prefixes, objects, focusedKey, loading, loadingPage, truncated, thumbnails, onNavigatePrefix, onFocusObject, onEnsureThumbnail, onScrollNearBottom })`.

- [ ] **Step 1: Write the failing test** — `frontend/src/components/oss/__tests__/OSSObjectGrid.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSObjectGrid } from "../OSSObjectGrid";
import type { oss_svc } from "../../../../wailsjs/go/models";

beforeEach(() => {
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as never
  );
});
afterEach(() => vi.unstubAllGlobals());

function obj(key: string, over: Partial<oss_svc.ObjectItem> = {}): oss_svc.ObjectItem {
  return { key, size: 10, lastModified: 0, etag: "", storageClass: "", contentType: "", isPrefix: false, ...over } as oss_svc.ObjectItem;
}
const base = {
  loading: false, loadingPage: false, truncated: false, focusedKey: null as string | null,
  thumbnails: {}, onNavigatePrefix: vi.fn(), onFocusObject: vi.fn(), onEnsureThumbnail: vi.fn(), onScrollNearBottom: vi.fn(),
};

describe("OSSObjectGrid", () => {
  it("renders folder and object tiles; single-click a tile focuses it, double-click a folder navigates", () => {
    const onFocusObject = vi.fn(), onNavigatePrefix = vi.fn();
    render(<OSSObjectGrid {...base} prefixes={["docs/sub/"]} objects={[obj("docs/a.txt")]} onFocusObject={onFocusObject} onNavigatePrefix={onNavigatePrefix} />);
    fireEvent.click(screen.getByTestId("oss-grid-object-docs/a.txt"));
    expect(onFocusObject).toHaveBeenCalledWith("docs/a.txt");
    fireEvent.doubleClick(screen.getByTestId("oss-grid-folder-docs/sub/"));
    expect(onNavigatePrefix).toHaveBeenCalledWith("docs/sub/");
  });

  it("shows loading and empty states", () => {
    const { rerender } = render(<OSSObjectGrid {...base} prefixes={[]} objects={[]} loading />);
    expect(screen.getByTestId("oss-grid-loading")).toBeInTheDocument();
    rerender(<OSSObjectGrid {...base} prefixes={[]} objects={[]} />);
    expect(screen.getByTestId("oss-grid-empty")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/OSSObjectGrid.test.tsx`. Expected: unresolved import `../OSSObjectGrid`.

- [ ] **Step 3: Implement** — `frontend/src/components/oss/OSSObjectGrid.tsx`:

```tsx
import type React from "react";
import { useTranslation } from "react-i18next";
import { Folder } from "lucide-react";
import type { oss_svc } from "../../../wailsjs/go/models";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { formatBytes } from "@/lib/formatBytes";
import { shouldLoadNextPage } from "./OSSObjectList";
import { OSSThumbnail } from "./OSSThumbnail";

export interface OSSObjectGridProps {
  prefixes: string[];
  objects: oss_svc.ObjectItem[];
  focusedKey: string | null;
  loading: boolean;
  loadingPage: boolean;
  truncated: boolean;
  thumbnails: Record<string, string>;
  onNavigatePrefix: (prefix: string) => void;
  onFocusObject: (key: string) => void;
  onEnsureThumbnail: (key: string) => void;
  onScrollNearBottom: () => void;
}

export function OSSObjectGrid({
  prefixes,
  objects,
  focusedKey,
  loading,
  loadingPage,
  truncated,
  thumbnails,
  onNavigatePrefix,
  onFocusObject,
  onEnsureThumbnail,
  onScrollNearBottom,
}: OSSObjectGridProps) {
  const { t } = useTranslation();

  const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (shouldLoadNextPage(el.scrollTop, el.clientHeight, el.scrollHeight, truncated, loadingPage)) {
      onScrollNearBottom();
    }
  };

  if (loading) {
    return (
      <div className="p-3 text-xs text-muted-foreground" data-testid="oss-grid-loading">
        {t("oss.browser.loading")}
      </div>
    );
  }
  if (prefixes.length === 0 && objects.length === 0) {
    return (
      <div className="p-6 text-center text-xs text-muted-foreground" data-testid="oss-grid-empty">
        {t("oss.browser.emptyDir")}
      </div>
    );
  }

  const tile = "flex w-[150px] cursor-pointer flex-col gap-1 rounded border p-1.5 hover:bg-accent/50";

  return (
    <div className="min-h-0 flex-1 overflow-auto p-3" onScroll={handleScroll} data-testid="oss-object-grid">
      <div className="flex flex-wrap gap-3">
        {prefixes.map((p) => (
          <div
            key={p}
            className={`${tile} items-center justify-center`}
            onDoubleClick={() => onNavigatePrefix(p)}
            data-testid={`oss-grid-folder-${p}`}
          >
            <div className="flex aspect-square w-full items-center justify-center">
              <Folder className="size-8 text-warning" />
            </div>
            <span className="w-full truncate text-center text-xs" title={p}>
              {prefixLeafName(p)}
            </span>
          </div>
        ))}
        {objects.map((o) => (
          <div
            key={o.key}
            className={`${tile} ${o.key === focusedKey ? "ring-2 ring-primary" : ""}`}
            onClick={() => onFocusObject(o.key)}
            data-testid={`oss-grid-object-${o.key}`}
          >
            <div className="aspect-square w-full overflow-hidden rounded bg-muted/20">
              <OSSThumbnail
                objectKey={o.key}
                contentType={o.contentType}
                url={thumbnails[o.key]}
                onEnsure={() => onEnsureThumbnail(o.key)}
                className="size-full"
              />
            </div>
            <span className="w-full truncate text-xs" title={o.key}>
              {prefixLeafName(o.key)}
            </span>
            <span className="text-[10px] text-muted-foreground">{formatBytes(o.size)}</span>
          </div>
        ))}
      </div>
      {loadingPage && (
        <div className="p-2 text-center text-xs text-muted-foreground" data-testid="oss-grid-page-spinner">
          {t("oss.browser.loadingMore")}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/OSSObjectGrid.test.tsx`, then `npx tsc -b`, then `pnpm lint` (fix only this file).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/OSSObjectGrid.tsx frontend/src/components/oss/__tests__/OSSObjectGrid.test.tsx
git status --short
git commit -m "✨ OSS 网格视图（缩略图瓦片，仅浏览）"
```

---

### Task 8: Additive props — `OSSObjectList` focus + `OSSBreadcrumb` view toggle

**Files:**
- Modify: `frontend/src/components/oss/OSSObjectList.tsx`
- Modify: `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx`
- Modify: `frontend/src/components/oss/OSSBreadcrumb.tsx`
- Modify: `frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx`

**Interfaces:**
- Produces: `OSSObjectListProps` gains `focusedKey?: string | null` + `onFocusObject?: (key: string) => void`; `OSSBreadcrumbProps` gains `viewMode?: "list" | "grid"` + `onViewModeChange?: (m: "list" | "grid") => void`. All optional and additive; existing props/behavior/tests unchanged.

- [ ] **Step 1: Add the failing tests.**

Append to `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx` (reuse its existing `base`/`obj` helpers):
```tsx
it("single-click an object row focuses it; the focused row is marked; clicking the checkbox does NOT focus", () => {
  const onFocusObject = vi.fn();
  const onToggleSelect = vi.fn();
  render(<OSSObjectList {...base} onFocusObject={onFocusObject} onToggleSelect={onToggleSelect} focusedKey="docs/a.txt" objects={[obj("docs/a.txt", 1)]} />);
  const row = screen.getByTestId("oss-object-docs/a.txt");
  expect(row.className).toContain("bg-accent");
  fireEvent.click(row);
  expect(onFocusObject).toHaveBeenCalledWith("docs/a.txt");
  onFocusObject.mockClear();
  fireEvent.click(screen.getByTestId("oss-select-docs/a.txt")); // checkbox → select, NOT focus
  expect(onToggleSelect).toHaveBeenCalledWith("docs/a.txt");
  expect(onFocusObject).not.toHaveBeenCalled();
});
```

Append to `frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx`:
```tsx
it("renders a view toggle that fires onViewModeChange", () => {
  const onViewModeChange = vi.fn();
  render(<OSSBreadcrumb bucket="mb" prefix="" onNavigate={vi.fn()} onRefresh={vi.fn()} viewMode="list" onViewModeChange={onViewModeChange} />);
  fireEvent.click(screen.getByTestId("oss-view-grid"));
  expect(onViewModeChange).toHaveBeenCalledWith("grid");
});
```

- [ ] **Step 2: Run — fails** — `pnpm test src/components/oss/__tests__/OSSObjectList.test.tsx src/components/oss/__tests__/OSSBreadcrumb.test.tsx`. Expected: `onFocusObject`/`focusedKey` not accepted; no `oss-view-grid` testid.

- [ ] **Step 3a: Implement `OSSObjectList` focus** — in `frontend/src/components/oss/OSSObjectList.tsx`:
  - Extend the props interface with `focusedKey?: string | null;` and `onFocusObject?: (key: string) => void;`, and destructure them.
  - The object `<tr>` (currently `className="group hover:bg-accent/50"` at line 108) becomes clickable + highlightable:
  ```tsx
  <tr
    key={o.key}
    className={`group cursor-pointer hover:bg-accent/50 ${o.key === focusedKey ? "bg-accent" : ""}`}
    onClick={() => onFocusObject?.(o.key)}
    data-testid={`oss-object-${o.key}`}
  >
  ```
  - The checkbox `<td>` must `stopPropagation` so toggling select does NOT also focus the row:
  ```tsx
  <td className="px-2 py-1" onClick={(e) => e.stopPropagation()}>
    <Checkbox
      checked={selection.has(o.key)}
      onCheckedChange={() => onToggleSelect(o.key)}
      data-testid={`oss-select-${o.key}`}
    />
  </td>
  ```
  - The download button's `onClick` also stops propagation (so download doesn't focus):
  ```tsx
  onClick={(e) => {
    e.stopPropagation();
    onDownload(o.key);
  }}
  ```

- [ ] **Step 3b: Implement `OSSBreadcrumb` view toggle** — in `frontend/src/components/oss/OSSBreadcrumb.tsx`:
  - Add to the props interface: `viewMode?: "list" | "grid";` and `onViewModeChange?: (m: "list" | "grid") => void;`, and destructure them.
  - Import the icons: `import { RefreshCw, Upload, List, LayoutGrid } from "lucide-react";`
  - Before the upload button, add the toggle (only when both props are provided):
  ```tsx
  {viewMode && onViewModeChange && (
    <div className="flex shrink-0 overflow-hidden rounded border">
      <button
        type="button"
        className={`px-1.5 py-1 ${viewMode === "list" ? "bg-accent" : "text-muted-foreground"}`}
        onClick={() => onViewModeChange("list")}
        title={t("oss.view.list")}
        data-testid="oss-view-list"
      >
        <List className="size-3" />
      </button>
      <button
        type="button"
        className={`px-1.5 py-1 ${viewMode === "grid" ? "bg-accent" : "text-muted-foreground"}`}
        onClick={() => onViewModeChange("grid")}
        title={t("oss.view.grid")}
        data-testid="oss-view-grid"
      >
        <LayoutGrid className="size-3" />
      </button>
    </div>
  )}
  ```

- [ ] **Step 4: Run — passes** — `pnpm test src/components/oss/__tests__/OSSObjectList.test.tsx src/components/oss/__tests__/OSSBreadcrumb.test.tsx` (new AND existing tests in both files green), then `npx tsc -b`, then `pnpm lint` (fix only the two component files).

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/oss/OSSObjectList.tsx frontend/src/components/oss/__tests__/OSSObjectList.test.tsx frontend/src/components/oss/OSSBreadcrumb.tsx frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx
git status --short
git commit -m "✨ OSS 列表行焦点 + 面包屑视图切换（附加 props）"
```

---

### Task 9: Wire `OSSBrowserPanel` (view swap · detail pane · share dialog · single-object delete) + full-suite gate

**Files:**
- Modify: `frontend/src/components/query/OSSBrowserPanel.tsx`
- Modify: `frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx`

**Interfaces:**
- Consumes: everything from Tasks 3–8 + `ossTransferStore.startDownload` (P3b-2), `useResizeHandle` (`@opskat/ui`), `ConfirmDialog` (already imported).

- [ ] **Step 1: Add the failing test** — the panel test ALREADY imports `OSSListBuckets`, `OSSBrowserPanel`, `useTabStore`, `useOssBrowserStore`, `screen`/`fireEvent`/`waitFor`, and stubs `IntersectionObserver` may be absent — add a stub in this file's `beforeEach` if the new render path touches a thumbnail. Add ONLY the new case (do not re-import already-imported symbols):

```tsx
it("toggles to grid view and opens the detail pane on single-click", async () => {
  vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
  render(<OSSBrowserPanel tabId={TAB} />);
  await screen.findByTestId("oss-bucket-b1");
  useOssBrowserStore.setState((s) => ({
    tabs: {
      ...s.tabs,
      [TAB]: {
        ...s.tabs[TAB],
        currentBucket: "b1",
        currentPrefix: "docs/",
        listing: { objects: [{ key: "docs/a.txt", size: 1, lastModified: 0, etag: "", storageClass: "", contentType: "", isPrefix: false }], prefixes: [], truncated: false, cursor: "" },
      },
    },
  }) as never);
  // switch to grid
  fireEvent.click(await screen.findByTestId("oss-view-grid"));
  expect(await screen.findByTestId("oss-object-grid")).toBeInTheDocument();
  // focus the object → detail pane opens
  fireEvent.click(screen.getByTestId("oss-grid-object-docs/a.txt"));
  expect(await screen.findByTestId("oss-object-detail")).toBeInTheDocument();
});
```

(Add an `IntersectionObserver` stub to this file's `beforeEach` — copy the class stub from Task 4's test — so the grid's thumbnails don't crash on `new IntersectionObserver`.)

- [ ] **Step 2: Run — fails** — `pnpm test src/components/query/__tests__/OSSBrowserPanel.test.tsx`. Expected: no `oss-view-grid`/`oss-object-grid`/`oss-object-detail` (not wired).

- [ ] **Step 3: Implement — wire the panel.** In `frontend/src/components/query/OSSBrowserPanel.tsx`:

1. Extend imports:
```tsx
import { useResizeHandle, ConfirmDialog, Button } from "@opskat/ui"; // (useResizeHandle already imported)
import { useOssTransferStore } from "@/stores/ossTransferStore";
import { OSSObjectGrid } from "@/components/oss/OSSObjectGrid";
import { OSSObjectDetail } from "@/components/oss/OSSObjectDetail";
import { OSSPresignDialog } from "@/components/oss/OSSPresignDialog";
```
2. Add store selectors + transfer download (after the existing browser-store selectors, ~line 32):
```tsx
const setViewMode = useOssBrowserStore((s) => s.setViewMode);
const focusObject = useOssBrowserStore((s) => s.focusObject);
const ensureThumbnail = useOssBrowserStore((s) => s.ensureThumbnail);
const deleteObject = useOssBrowserStore((s) => s.deleteObject);
const startDownload = useOssTransferStore((s) => s.startDownload);
```
3. Add local dialog/confirm state (next to `confirmOpen`):
```tsx
const [shareOpen, setShareOpen] = useState(false);
const [detailDeleteOpen, setDetailDeleteOpen] = useState(false);
```
4. Derive the focused object + detail-pane resize (after `rows` useMemo):
```tsx
const focusedObject = useMemo(
  () => state?.listing?.objects.find((o) => o.key === state.focusedKey) ?? null,
  [state?.listing, state?.focusedKey]
);
const detailRef = useRef<HTMLDivElement>(null);
const { size: detailWidth, handleMouseDown: handleDetailResize } = useResizeHandle({
  defaultSize: 320,
  minSize: 260,
  maxSize: 520,
  reverse: true,
  targetRef: detailRef,
});
```
5. Handlers (after `onSelectBucket`):
```tsx
const onDetailDownload = useCallback(() => {
  if (!assetId || !state?.currentBucket || !state.focusedKey) return;
  void startDownload(tabId, assetId, state.currentBucket, state.focusedKey).catch(() =>
    toast.error(t("oss.transfer.downloadFailed"))
  );
}, [assetId, state?.currentBucket, state?.focusedKey, startDownload, tabId, t]);

const confirmDetailDelete = async () => {
  setDetailDeleteOpen(false);
  if (!state?.focusedKey) return;
  try {
    await deleteObject(tabId, state.focusedKey);
    notifySuccess(t("oss.browser.deleteSuccess"));
  } catch (e) {
    toast.error(`${t("oss.browser.deleteFailed")}: ${String(e)}`);
  }
};
```
6. In the right pane's `state?.currentBucket ? (...)` branch, pass the new props to the breadcrumb and object list, and swap list/grid on `state.viewMode`:
```tsx
<OSSBreadcrumb
  bucket={state.currentBucket}
  prefix={state.currentPrefix}
  onNavigate={onNavigate}
  onRefresh={() => void refresh(tabId).catch(fail)}
  onUpload={onUpload}
  viewMode={state.viewMode}
  onViewModeChange={(m) => setViewMode(tabId, m)}
/>
{/* ... existing selection bar ... */}
{state.viewMode === "grid" ? (
  <OSSObjectGrid
    prefixes={state.listing?.prefixes ?? []}
    objects={state.listing?.objects ?? []}
    focusedKey={state.focusedKey}
    loading={state.loading.listing}
    loadingPage={state.loading.page}
    truncated={state.listing?.truncated ?? false}
    thumbnails={state.thumbnails}
    onNavigatePrefix={onNavigate}
    onFocusObject={(key) => focusObject(tabId, key)}
    onEnsureThumbnail={(key) => void ensureThumbnail(tabId, key)}
    onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)}
  />
) : (
  <OSSObjectList
    prefixes={state.listing?.prefixes ?? []}
    objects={state.listing?.objects ?? []}
    selection={state.selection}
    loading={state.loading.listing}
    loadingPage={state.loading.page}
    truncated={state.listing?.truncated ?? false}
    focusedKey={state.focusedKey}
    onNavigatePrefix={onNavigate}
    onToggleSelect={(key) => toggleSelect(tabId, key)}
    onFocusObject={(key) => focusObject(tabId, key)}
    onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)}
    onDownload={onDownload}
  />
)}
```
   (Keep the existing `onUpload`/`onDownload` handlers from P3b-2 as-is; `onDownload` here is the P3b-2 per-row download.)
7. Add the detail pane as a THIRD column inside `<div className="flex min-h-0 flex-1">` (the outer row that holds sidebar + resize + right pane), AFTER the right-pane `</div>`, rendered only when a focused object exists:
```tsx
{focusedObject && (
  <>
    <div
      className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent"
      onMouseDown={handleDetailResize}
    />
    <div ref={detailRef} className="shrink-0 border-l" style={{ width: detailWidth }}>
      <OSSObjectDetail
        object={focusedObject}
        thumbnailUrl={state?.thumbnails[focusedObject.key]}
        onEnsureThumbnail={() => void ensureThumbnail(tabId, focusedObject.key)}
        onShare={() => setShareOpen(true)}
        onDownload={onDetailDownload}
        onDelete={() => setDetailDeleteOpen(true)}
        onClose={() => focusObject(tabId, null)}
      />
    </div>
  </>
)}
```
8. Add the share dialog + single-object delete confirm near the existing `ConfirmDialog` (before the component's closing `</div>`):
```tsx
{focusedObject && (
  <OSSPresignDialog
    open={shareOpen}
    onOpenChange={setShareOpen}
    assetId={assetId}
    bucket={state?.currentBucket ?? ""}
    objectKey={focusedObject.key}
  />
)}
<ConfirmDialog
  open={detailDeleteOpen}
  onOpenChange={setDetailDeleteOpen}
  title={t("oss.browser.deleteConfirmTitle")}
  description={t("oss.browser.deleteConfirmOne", { key: state?.focusedKey ?? "" })}
  cancelText={t("action.cancel")}
  confirmText={t("action.confirm")}
  onConfirm={() => void confirmDetailDelete()}
/>
```
   Ensure `useMemo`/`useRef`/`useCallback` are imported (the panel already imports them).

- [ ] **Step 4: Run — passes** — `pnpm test src/components/query/__tests__/OSSBrowserPanel.test.tsx`, then the **FULL suite** `pnpm test`, then `npx tsc -b`, then `pnpm lint` (`eslint --fix` the panel if needed).
  - **CONCURRENT-WORK NOTE:** the full suite runs against a tree that also contains the parallel session's uncommitted etcd/i18n edits. Your PASS criterion is: every OSS/transfer/formatBytes/coordinator/registry/`objectContentType`/i18n test green. Failures in `src/__tests__/EtcdTreePane.test.tsx`, `src/__tests__/EtcdPanel.test.tsx`, or an etcd-key i18n asymmetry are EXTERNAL — record which files failed in your report and proceed; do NOT edit any etcd or non-`oss` i18n file to "fix" them.

- [ ] **Step 5: Commit**

```bash
git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
git add frontend/src/components/query/OSSBrowserPanel.tsx frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx
git status --short
git commit -m "✨ 面板接线 OSS 详情面板/分享对话框/网格切换/单对象删除"
```

---

## Final verification (after Task 9)

- [ ] From `frontend/`: `pnpm test` (full — all OSS/i18n green; note any external etcd failures) · `npx tsc -b` · `pnpm lint` (no new errors in the P3b-3 files), then `git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml` and confirm `git status --short` stages nothing of yours beyond the committed work (the concurrent etcd/i18n `M` entries + untracked ai-follow spec remain, untouched).
- [ ] **Observational verification (spec §9 — observe, don't assert; requires a local MinIO OSS asset):** run the app (`make dev`), connect OSS, select a bucket. (1) Toggle **网格** → tiles render, image objects show thumbnails (presigned GET), non-images show type-icons; scroll a truncated listing → next page loads. (2) Single-click an object (list or grid) → the detail pane opens with metadata + preview; **下载** streams to the dock (P3b-2); **删除** → confirm → object gone + pane closes. (3) **分享链接** → pick GET/PUT + expiry → **生成** → URL appears → **复制**; verify a GET link actually downloads the object and a PUT link accepts an upload (the presign-context checks deferred from P3a). Read `logs/opskat.log` + `opskat.db`.

## Spec → task coverage map

- §1 backend surface (Presign/Stat/Remove) → consumed by T3 (store), T6 (dialog); no new backend.
- §2 locked decisions: combined spec (all tasks); single-click→detail (T8 list, T7 grid, T9 wiring); browse-only grid (T7); lazy per-visible thumbnails (T4 + T3 `ensureThumbnail`); GET+PUT (T6); detail=listing ObjectItem (T5/T9); image-only + silent fallback (T1/T4/T3); default expiry (T3); single-object delete (T3 `deleteObject`, T9 confirm); state in ossBrowserStore (T3).
- §3 state model → T3. §4 components → T1/T4/T5/T6/T7 (new) + T8 (additive). §5 data flows → T3 + T9. §6 layout (3-pane, resizable detail, grid tiles) → T9 + T7. §7 error/empty/loading → T3 (silent thumb, delete error) + T5/T6/T7 (placeholders/toast). §8 reuse + i18n → all + T2. §9 testing + deferrals → each task's tests + Final verification. §10 concurrency/env → Global Constraints + every task's commit discipline.
