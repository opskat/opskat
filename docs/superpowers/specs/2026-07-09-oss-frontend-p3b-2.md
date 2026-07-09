# OSS Object Browser — P3b-2 (Transfers) Design Spec

**Branch:** `feature/oss-asset-type` (P1 + P2 + P3a + P3b-1 all done, merge-ready, NOT merged). P3b-2 base = P3b-1 head.
**Scope:** the frontend transfer layer for the OSS object browser — upload (toolbar button + drag-drop), single-object download, a live progress dock, and auto-refresh on upload-complete — over the P3a streaming bindings.
**Out of scope (later phases / deferred):** retry, multi-object download, new-folder / grid-toggle / rename / move, the transfer TOCTOU cancel race, half-file-on-cancel cleanup.

---

## 1. What is already locked (grounded against the code)

### 1.1 P3a backend bindings (positional args, NOT DTOs)
From `internal/app/oss/oss_transfer.go`, generated into `frontend/wailsjs/go/oss/OSS.d.ts`:

```ts
OSSUploadObject(assetId: number, bucket: string, keyPrefix: string): Promise<string[]>
OSSUploadObjectPath(assetId: number, bucket: string, key: string, localPath: string): Promise<string>
OSSDownloadObject(assetId: number, bucket: string, key: string): Promise<string>
OSSCancelTransfer(transferId: string): Promise<void>
```

- `OSSUploadObject` opens a **native multi-select dialog** (Go side), derives each object key as `keyPrefix + basename` (via `deriveUploadKey`, already prefix-normalized), spawns one goroutine per file, and returns **one transferId per selected file**. Dialog-cancel → `[]` (empty array), no error.
- `OSSUploadObjectPath` takes an explicit `key` + OS `localPath` (no dialog) — this is the **drag-drop** entry. Returns a single transferId.
- `OSSDownloadObject` opens a **native save dialog** (`DefaultFilename = path.Base(key)`). Dialog-cancel → `""` (empty string), no error. Returns a single transferId.
- `OSSCancelTransfer` is idempotent (unknown/finished id = no-op, resolves); errors only on empty id. Cancels via a `context.CancelFunc` stored server-side.

### 1.2 Progress event (wire payload)
Emitted on `"transfer:progress:" + transferId` via Wails `EventsEmit`. Payload = `transfer.Progress` (`internal/pkg/transfer/transfer.go`), camelCase JSON:

```ts
interface OssTransferProgressEvent {
  transferId: string;
  status: "progress" | "done" | "error" | "cancelled";  // wire vocabulary
  currentFile: string;
  filesCompleted: number;
  filesTotal: number;
  bytesDone: number;
  bytesTotal: number;
  speed: number;        // bytes/sec, average since start
  error?: string;
}
```

- Throttle: shared `transfer.Reporter`, `100ms` — only `"progress"` events are throttled; `"done"`/`"error"`/`"cancelled"` bypass and emit immediately (exactly once).
- **OSS emits an EXPLICIT `"cancelled"` status** (backend checks `errors.Is(err, context.Canceled)` in `emitTerminal`) — unlike SFTP, whose frontend infers cancellation from a `"context canceled"` substring in the `"error"` payload. Our handler switches on `"cancelled"` directly; **no substring sniffing.**
- There is **no generated TS type** for `transfer.Progress` (it's only an event payload, never a bound-method return) — we hand-write the interface above, mirroring how `sftpStore` types its handler.

### 1.3 Native OS file-drop mechanism
- `main.go:204`: `DragAndDrop: { EnableFileDrop: true, DisableWebViewDrop: true }` — Wails delivers **real OS paths** on drop; the webview's own drop is disabled.
- Wails runtime: `OnFileDrop(cb: (x, y, paths: string[]) => void, useDropTarget: boolean)` / `OnFileDropOff()`. **`OnFileDrop` is a global singleton** — only one active handler; registering a second clobbers the first.
- `frontend/src/components/terminal/terminalFileDropCoordinator.ts` already arbitrates this singleton: surfaces register `{ getRect, <upload cb> }`; on drop the coordinator hit-tests `(x,y)` against registered rects and routes to the first hit; it turns the single `OnFileDrop` on when ≥1 target is registered and off at zero. It currently hardcodes two target types (terminal, file-manager) and has a test (`terminalFileDropCoordinator.test.ts` + `resetTerminalFileDropCoordinatorForTest`). **The OSS overlay MUST go through this coordinator** — it cannot own its own `OnFileDrop`.

### 1.4 Why NOT reuse sftpStore / TransferSection
`frontend/src/stores/sftpStore.ts` keys transfers by `{tabId, sessionId}` (terminal-session-scoped) and `TransferSection.tsx` imports `useSFTPStore` directly with hardcoded `sftp.*` i18n and a bespoke progress bar (no shared primitive). Neither maps onto OSS's `assetId + bucket` context. Per **Reuse-first**, we reuse the *wire protocol* and the *visual idiom* (a `div` track + `@opskat/ui` `ScrollArea`/`Button`), and build an OSS-specific store + dock — exactly as P3b-1 built OSS-specific components rather than lifting SFTP.

---

## 2. Locked UX decisions (this brainstorm)

| Decision | Choice |
|---|---|
| Upload entry points | **Toolbar button + drag-drop overlay** (both) |
| Download | **Single object per row** (per-row action → one native save dialog) |
| Retry | **Deferred** (no retry binding; user re-invokes) |
| Auto-refresh | **On upload `"done"`, if the transfer's target prefix === the tab's current prefix, re-list** so the new object appears. Downloads never refresh. |
| Status vocabulary | Frontend store mirrors sftpStore's **renamed** enum: `"active" | "done" | "error" | "cancelled"` (wire `"progress"` → `"active"`). |
| `"done"` rows | Auto-remove after **5s** (mirrors sftpStore); terminal `error`/`cancelled` rows persist until cleared. |
| Concurrency | No frontend throttle — the backend spawns a goroutine per file; the dock displays all. |
| File-drop coordinator | **Add** a generic `registerFileDropTarget({getRect, onDrop})` to the coordinator, hit-tested **after** the existing terminal + file-manager targets — their maps, precedence, and tests stay untouched (avoids regressing the file-manager-over-terminal ordering). |

---

## 3. Architecture & units

All new frontend units live under `frontend/src/`. The container is the existing P3b-1 `OSSBrowserPanel`.

### 3.1 `stores/ossTransferStore.ts` (new Zustand store, keyed by tabId)
State: `Record<tabId, { transfers: Record<transferId, OssTransfer> }>` (per-tab isolation, mirroring `ossBrowserStore`), where:

```ts
interface OssTransfer {
  transferId: string;
  tabId: string;
  direction: "upload" | "download";
  name: string;            // basename (from currentFile / derived key)
  targetPrefix?: string;   // upload only — the prefix the object lands in (for auto-refresh compare)
  bytesDone: number;
  bytesTotal: number;
  speed: number;
  status: "active" | "done" | "error" | "cancelled";
  error?: string;
}
```

Actions (all keyed by tabId via a `patch(tabId, fn)` helper that only mutates an existing per-tab entry, same discipline as `ossBrowserStore`):
- `startUpload(tabId, assetId, bucket, prefix)` → `OSSUploadObject(assetId, bucket, prefix)`; for each returned id, insert an `active` upload row (`targetPrefix = prefix`) and `subscribeProgress(id)`. Empty array (dialog-cancel) → no-op. Binding reject → rethrow (panel toasts).
- `startUploadPath(tabId, assetId, bucket, prefix, localPath)` → derive `key = prefix + basename(localPath)`, call `OSSUploadObjectPath(assetId, bucket, key, localPath)`; insert row + subscribe. (Used by the drop overlay, once per dropped path.)
- `startDownload(tabId, assetId, bucket, key)` → `OSSDownloadObject(...)`; empty id (dialog-cancel) → no-op; else insert a `download` row + subscribe.
- `subscribeProgress(transferId)` → `EventsOn("transfer:progress:" + transferId, handler)`. Handler: ignore if the row is gone; on `"progress"` merge `bytesDone/bytesTotal/speed/name` (status stays `"active"`); on `"done"`/`"error"`/`"cancelled"` set the mapped status, `EventsOff(eventName)`, and — for `"done"` — schedule auto-remove after 5s AND run the upload-complete hook (§3.6). **Explicit `"cancelled"` case; no substring inference.**
- `cancel(transferId)` → `OSSCancelTransfer(transferId)` (fire-and-forget; the terminal event flips status).
- `clear(transferId)` / `clearCompleted(tabId)` (removes non-`active` rows).
- Registered `tabCloseHook` (like `ossBrowserStore`) — on tab close, `EventsOff` every live subscription for that tab and drop its slice.

### 3.2 `components/oss/OSSTransferDock.tsx` (presentational-ish)
Props: `{ transfers: OssTransfer[]; onCancel: (id) => void; onClear: (id) => void; onClearCompleted: () => void }`. Renders a collapsible bottom dock (auto-shown by the panel only when `transfers.length > 0`). Per row: direction icon (`Upload`/`Download`), `name`, a bespoke progress bar (`div.h-1` track + inner fill `width: ${percent}%`, `percent = bytesTotal ? round(bytesDone/bytesTotal*100) : 0`), `formatBytes(bytesDone)/formatBytes(bytesTotal)`, `formatSpeed(speed)`, a status icon (`Loader2` spin / `CheckCircle2` / `XCircle`), and one button that is **cancel** (`X`) while `active`, else **clear**. Header has a `清除已完成` button. Uses `@opskat/ui` `ScrollArea` + `Button`.

### 3.3 `lib/formatBytes.ts` (shared util) + `formatSpeed`
Lift the P3b-1 `formatBytes` out of `OSSObjectList.tsx` into `frontend/src/lib/formatBytes.ts` (re-export from the old site or update its import — one call site) and add `formatSpeed(bytesPerSec): string` (`formatBytes(n) + "/s"`, `0 → "0 B/s"`). Both pure, unit-tested. (Avoids a parallel byte formatter — Reuse-first.)

### 3.4 File-drop coordinator: additive generic target
**Additive only — the two existing typed maps (`terminalTargets`, `fileManagerTargets`), their register fns, hit-test precedence, and existing test stay untouched.** Add a third `genericTargets: Map<symbol, { getRect, onDrop: (paths: string[]) => void }>` and a new `export function registerFileDropTarget(target): () => void` (returns an unregister fn, same shape as the existing two). Extend `handleFileDrop` to check the generic targets **after** the file-manager and terminal targets (so a drop only reaches OSS when neither of those claims the point — preserves the current file-manager-over-terminal ordering exactly, since the OSS panel lives in a different tab and never overlaps them anyway). `syncWailsFileDropListener` already turns the single `OnFileDrop` on/off by total target count — extend `targetCount()` to include `genericTargets.size`. Add one focused test for the generic path (register → drop inside its rect routes to `onDrop`; unregister → `OnFileDrop` off at zero targets); the existing terminal/file-manager assertions are unchanged. (Rename of the file/module is out of scope — keep `terminalFileDropCoordinator.ts` to avoid churning the terminal call sites.)

### 3.5 `components/oss/OSSDropOverlay.tsx`
A drag-mask over the panel's content area. On mount, `registerFileDropTarget({ getRect: () => contentRef.current?.getBoundingClientRect(), onDrop: (paths) => paths.forEach(p => startUploadPath(tabId, assetId, bucket, currentPrefix, p)) })`; unregister on unmount. Shows the mask (`oss.transfer.dropHint`) while a drag is over the region — driven by the browser's `dragenter`/`dragleave`/`dragover` events on the panel (visual only; the actual path delivery is via the Wails coordinator, since `DisableWebViewDrop` means the webview drop event carries no files). Only active when a bucket is selected.

### 3.6 `components/query/OSSBrowserPanel.tsx` (extend the P3b-1 container)
- Subscribe to `useOssTransferStore((s) => s.tabs[tabId]?.transfers)`; render `OSSTransferDock` below the object list when non-empty; render `OSSDropOverlay` over the content when a bucket is selected.
- Wire the breadcrumb's new `onUpload` → `startUpload(tabId, assetId, bucket, currentPrefix)` (`.catch(toast.error)`); wire the object list's new `onDownload(key)` → `startDownload(tabId, assetId, bucket, key)` (`.catch(toast.error)`).
- **Auto-refresh hook (implemented in `ossTransferStore`, not the panel):** the transfer store's `"done"` handler — for an upload whose `targetPrefix === useOssBrowserStore.getState().tabs[tabId]?.currentPrefix` — calls `useOssBrowserStore.getState().refresh(tabId)` (store→store via `getState`; `ossTransferStore` imports `ossBrowserStore`, not vice-versa, so no import cycle and no React coupling; fires regardless of panel mount state). Refresh failure is caught to a non-blocking `toast.error` inside the hook (a stale-but-present list is acceptable — the transfer itself succeeded). The panel does not implement this; it only renders the results.

### 3.7 Additive props on P3b-1 components (no reshape)
- `OSSBreadcrumb`: add optional `onUpload?: () => void` → renders a `上传` button beside the existing refresh button. All existing props/behavior unchanged.
- `OSSObjectList`: add optional `onDownload?: (key: string) => void` → renders a hover download icon-button on **object** rows only (not folder rows). All existing props/behavior unchanged.

### 3.8 i18n `oss.transfer.*` (en + zh-CN, lockstep)
Keys (both locales, idiomatic): `upload`, `download`, `transfers` (dock title), `clearCompleted`, `cancel`, `clear`, `dropHint`, `uploadFailed`, `downloadFailed`, `refreshAfterUploadFailed`. Added up-front so the full suite stays green at every commit (same pattern as P3b-1 T2).

---

## 4. Data flow

- **Upload (button):** click `上传` → `startUpload(tab, assetId, bucket, prefix)` → native multi-select (Go) → `string[]` ids → N `active` rows + N `EventsOn` subscriptions → progress events stream in → each `"done"` removes its row after 5s and, if `targetPrefix === currentPrefix`, refreshes the list.
- **Upload (drag-drop):** drag files over the panel → overlay mask shows → drop → Wails coordinator delivers OS paths → `startUploadPath` per path → same subscription/refresh flow.
- **Download:** hover an object row → click download icon → `startDownload` → native save dialog (Go) → id → `active` download row → progress → `"done"`/`"error"`/`"cancelled"`.
- **Cancel:** click the row's cancel (X) while `active` → `OSSCancelTransfer(id)` → backend cancels the ctx → explicit `"cancelled"` terminal event → row flips to `cancelled` (persists until cleared).

---

## 5. Error / empty / loading handling

- Start-binding rejection (`OSSUploadObject`/`OSSDownloadObject`/`OSSUploadObjectPath`) → `toast.error(t("oss.transfer.uploadFailed"|"downloadFailed"))`; dialog-cancel (`[]` / `""`) → silent no-op (not an error).
- Progress `"error"` → the row shows `error` + a clear button; no toast (the dock is the surface).
- Auto-refresh failure → non-blocking `toast.error(t("oss.transfer.refreshAfterUploadFailed"))`; the transfer stays `"done"`.
- No swallowed errors in the AGENTS.md sense: every store action that can reject sets no silent state — start actions rethrow to the panel; the refresh hook's catch is a *concrete* recovery (list is stale, not corrupt) and surfaces a toast.
- Empty dock (no transfers) → not rendered at all (panel gates on `transfers.length > 0`).

---

## 6. Testing strategy

**Unit / store (happy-dom + vitest, mocked oss binder [already in setup.ts from P3b-1 T7] + mocked `wailsjs/runtime/runtime` `EventsOn`/`EventsOff`):**
- `ossTransferStore`: `startUpload` with 2 returned ids → 2 `active` rows + 2 subscriptions; dialog-cancel (`[]`) → no rows; a `"progress"` event merges numeric fields and keeps `active`; `"done"` → `done` + `EventsOff` + auto-remove after fake-timer 5s + **fires `ossBrowserStore.refresh` when `targetPrefix === currentPrefix`, and NOT when it differs**; explicit `"cancelled"` event → `cancelled`; `"error"` → `error` with message; `cancel(id)` → `OSSCancelTransfer(id)` called; per-tab isolation (two tabIds don't clobber); tab-close hook `EventsOff`s + drops the slice.
- `formatBytes` (moved) + `formatSpeed`: exact-value cases.
- File-drop coordinator: hit-test routes a drop to the correct registered target's `onDrop`; single `OnFileDrop` toggled on/off at 1/0 targets; the two legacy adapters still route as before (preserve existing test assertions).

**Component (happy-dom):**
- `OSSTransferDock`: renders rows with %/speed; the row button calls `onCancel` while `active` and `onClear` otherwise; header `清除已完成` calls `onClearCompleted`.
- `OSSObjectList` (additive): a download icon on object rows fires `onDownload(key)`; folder rows have none.
- `OSSBreadcrumb` (additive): the `上传` button fires `onUpload`.

**Deferred to MANUAL / live-MinIO observation (happy-dom cannot observe — spec §9 of the browser design):** the native multi-select / save dialogs, real desktop drag-drop path delivery, real progress streaming/throttling, and the **upload-cancel context-propagation check** (P3a handoff: confirm minio `PutObject` propagates `context.Canceled` through `errors.Is` so the cancel button actually flips an in-flight upload to `cancelled`). TOCTOU cancel race + half-file-on-cancel cleanup remain deferred (mirror SFTP).

---

## 7. Reuse map

- Wire protocol + 100ms `transfer.Reporter` (backend, shared with SFTP/ZMODEM) — consumed, not rebuilt.
- `OnFileDrop` singleton arbitration — **extended** the existing `terminalFileDropCoordinator` with an additive generic target (no second `OnFileDrop`; existing typed targets + precedence untouched).
- `formatBytes` — lifted to a shared `lib/formatBytes.ts` (no parallel formatter).
- `@opskat/ui` `ScrollArea` / `Button`, `lucide-react` icons — the only genuinely reusable UI pieces from the SFTP dock; the progress-bar markup is bespoke (no shared primitive exists).
- `ossBrowserStore.refresh` / `currentPrefix` — the auto-refresh cross-store contract.
- P3b-1 additive-prop pattern (`OSSBreadcrumb`/`OSSObjectList` gain optional callbacks; no reshape).

---

## 8. Coverage checklist (design → later plan tasks)

- Progress store + subscription + explicit-cancelled + auto-refresh hook → `ossTransferStore.ts`.
- Shared `formatBytes` lift + `formatSpeed`.
- File-drop coordinator: additive generic `registerFileDropTarget` (existing typed targets, precedence, and tests untouched) + one new test for the generic path.
- `OSSTransferDock` + `OSSDropOverlay`.
- Additive download action (`OSSObjectList`) + upload button (`OSSBreadcrumb`).
- Panel integration (dock + overlay + handlers + auto-refresh wiring).
- i18n `oss.transfer.*`.

Single implementation plan (≈7–8 TDD tasks), no sub-split — comparable in size to P3b-1.
