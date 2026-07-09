# OSS Object Browser — P3b-3 (Detail · Share · Grid) Design Spec

**Branch:** `feature/oss-asset-type` (P1+P2+P3a+P3b-1+P3b-2 done, all merge-ready, unmerged).
**Base:** P3b-2 head `c88639d4`.
**Goal:** Complete the OSS object browser with (1) a right-side **object detail pane**, (2) a **presigned-URL share dialog**, and (3) a **list/grid view toggle** with lazy image thumbnails. Entirely frontend — every capability is already backed by P3a bindings; no backend work.

Design authority: the opskat.pen frames `oss-object-detail` (`TGA8D`), `oss-presigned-dialog` (`DVIXO`), and `oss-grid-view` (`g81QT9`). This spec grounds them in the P3a binding surface and the P3b-1/P3b-2 component/store patterns.

---

## §1. Backend surface (all existing — NO new backend)

Bindings from `../../wailsjs/go/oss/OSS` (P3a, generated, gitignored):

- `OSSStatObject(req: ObjectRequest): Promise<ObjectItem>` — authoritative single-object metadata. **Not required for the common path** (the listing row already carries the same fields); reserved as an optional freshness fallback only.
- `OSSPresignGet(req: PresignRequest): Promise<string>` — presigned GET URL (download link + image thumbnail `src`).
- `OSSPresignPut(req: PresignRequest): Promise<string>` — presigned PUT URL (upload link).
- Reused: `OSSRemoveObject(req: ObjectRequest): Promise<void>` (detail-pane single-object delete), `OSSListObjects` (pagination, unchanged).

Wire shapes (camelCase over IPC):

```ts
// ObjectItem (from ListObjects / StatObject) — already consumed by P3b-1
interface ObjectItem { key: string; size: number; lastModified: number; etag: string; storageClass: string; contentType: string; isPrefix: boolean; }
interface ObjectRequest  { assetId: number; bucket: string; key: string; }
interface PresignRequest { assetId: number; bucket: string; key: string; expirySecs: number; } // expirySecs<=0 → server default (1h)
```

`lastModified` is int64 **seconds** (×1000 for JS `Date`), matching P3b-1's `OSSObjectList`.

---

## §2. Locked decisions

From the brainstorm (user-approved):

1. **One combined P3b-3 spec** — detail + share + grid ship together (~9–11 TDD tasks).
2. **Single-click opens detail.** Single-click an object row (list) or tile (grid) sets it as the *focused* object and opens the right detail pane. This is a NEW `focusedKey` concept, **distinct** from the existing `selection: Set` (multi-select-delete). Checkbox → multi-select (unchanged); double-click folder → navigate (unchanged).
3. **Browse-only grid.** Grid is a visual browse/preview mode: click a tile → detail pane (act there). Multi-select-delete stays **list-only** — grid tiles carry no checkbox and no selection bar.
4. **Lazy per-visible thumbnails.** An image object's thumbnail is presigned + loaded only when its row/tile scrolls into view (`IntersectionObserver`), and the URL is cached per object key so re-renders/scrollbacks don't re-presign.
5. **GET + PUT share toggle.** The share dialog offers both download (GET) and upload (PUT) links; both bindings exist.

Controller defaults (not separately asked; stated here as decisions):

6. **Detail data = the listing's `ObjectItem`.** No `OSSStatObject` call on focus — the row already holds every field the pane shows. `OSSStatObject` is left unused by P3b-3 (available for a future "refresh metadata" affordance).
7. **Thumbnails = `content-type` `image/*` only.** Everything else (and folders) renders a lucide type-icon; non-images never presign.
8. **Thumbnail presign uses the server default expiry** (`expirySecs: 0` → 1h). One presign per image per session; an expired cached URL simply 404s until the next `refresh` re-presigns (acceptable, best-effort).
9. **Thumbnail failure is silent** — fall back to the type-icon; do NOT toast. A preview is best-effort, not a user-initiated operation. (Share/delete/download failures, which ARE user-initiated, still toast.)
10. **Detail-pane delete = single-object.** A new store action `deleteObject(tabId, key)` removes exactly the focused object (`OSSRemoveObject`), then refreshes and clears focus — it does NOT touch the multi-select `selection`. Confirm via the existing `ConfirmDialog` with the `oss.browser.deleteConfirmOne` copy.
11. **Detail-pane state lives in `ossBrowserStore`** (per-tab), reusing its `patch`/`tabCloseHook` discipline — not a new store.

---

## §3. State model — `ossBrowserStore` per-tab additions

Extend `OssBrowserTabState` (created in P3b-1) with three fields, initialized in `emptyTabState`:

```ts
viewMode: "list" | "grid";          // default "list"
focusedKey: string | null;          // default null — the detail pane's object; null = pane closed
thumbnails: Record<string, string>; // default {} — object key → presigned GET URL (lazy)
```

New actions (all routed through the existing `patch(tabId, fn)` helper so they only mutate an existing slice):

```ts
setViewMode(tabId: string, mode: "list" | "grid"): void;
focusObject(tabId: string, key: string | null): void;         // opens/closes the detail pane
ensureThumbnail(tabId: string, key: string): Promise<void>;    // presign-once + cache; no-op if cached or already in flight
deleteObject(tabId: string, key: string): Promise<void>;       // OSSRemoveObject(key) → refresh → clear focus if focusedKey===key; rethrow on failure
```

Contracts:
- `ensureThumbnail` — if `thumbnails[key]` is already set OR a presign for `key` is in flight, return immediately (no duplicate binding call). Otherwise `OSSPresignGet({assetId, bucket, key, expirySecs: 0})`, then `patch` the URL into `thumbnails[key]`. On error: leave `thumbnails[key]` unset (the component keeps showing its icon) — swallow-with-icon-fallback is the intended behavior for this best-effort preview and is called out as an explicit exception to the no-swallow rule.
- `focusObject(tabId, null)` closes the pane. `navigateToPrefix`/`refresh` (P3b-1) must clear `focusedKey` when the focused key is no longer in the new listing (fold a `focusedKey` reset into those existing actions — clear it whenever the listing is replaced, simplest and correct).
- `deleteObject` — mirrors `deleteSelected`'s failure contract (set `error` + rethrow so the panel can toast); on success clears `focusedKey` and calls `refresh(tabId)`.
- `thumbnails` cache is intentionally NOT cleared on `refresh` (URLs stay valid ~1h; re-fetching the same keys is wasteful). It IS dropped with the slice on tab close.

---

## §4. Components

### New

- **`OSSObjectDetail.tsx`** (`components/oss/`) — presentational right pane. Props: `object: ObjectItem`, `assetId: number`, `bucket: string`, `thumbnailUrl?: string`, `onEnsureThumbnail: () => void`, `onShare: () => void`, `onDownload: () => void`, `onDelete: () => void`, `onClose: () => void`. Layout (per `oss-object-detail`): header row (basename + close `X`); thumbnail block (image → `OSSThumbnail`, else large type-icon); a metadata grid (name/key with copy, size via `formatBytes`, content-type, ETag, storage class, last-modified via `Date`); an action row — **生成分享链接** (primary → `onShare`), **下载** (→ `onDownload`), **删除** (destructive → `onDelete`). Copy-key via `notifyCopied`. Lighter than `EtcdKeyDetail` (no edit/save/history).

- **`OSSPresignDialog.tsx`** (`components/oss/`) — the share modal (Radix Dialog via `@opskat/ui`). Props: `open: boolean`, `onOpenChange: (v: boolean) => void`, `assetId: number`, `bucket: string`, `objectKey: string`. Local state: `method: "get" | "put"`, `expirySecs: number` (preset `900 | 3600 | 86400 | 604800`, default `3600`), `url: string`, `loading: boolean`. Layout (per `oss-presigned-dialog`): object header; **GET·下载 / PUT·上传** segmented toggle; **15分钟 / 1小时 / 24小时 / 7天** expiry buttons; a read-only URL textarea + **复制** (→ `notifyCopied`); a warning note; footer **重新生成 / 关闭 / 复制并关闭**. Generate calls `OSSPresignGet`/`OSSPresignPut({assetId, bucket, key, expirySecs})` per `method`; on error → `toast.error(oss.share.generateFailed)`. Changing method/expiry clears the stale `url` (forces a fresh generate). Opening the dialog resets state; it does NOT auto-generate (user clicks 生成/重新生成).

- **`OSSObjectGrid.tsx`** (`components/oss/`) — presentational tile view (browse-only). Props mirror the data half of `OSSObjectList`: `prefixes: string[]`, `objects: ObjectItem[]`, `focusedKey: string | null`, `loading: boolean`, `loadingPage: boolean`, `truncated: boolean`, `thumbnails: Record<string,string>`, `onNavigatePrefix: (p) => void`, `onFocusObject: (key) => void`, `onEnsureThumbnail: (key) => void`, `onScrollNearBottom: () => void`. Renders folder tiles (folder icon + `prefixLeafName`) and object tiles (`OSSThumbnail` for images / type-icon otherwise, name + `formatBytes`). Single-click tile → `onFocusObject`; double-click folder → `onNavigatePrefix`; focused tile gets a ring/highlight. Reuses `shouldLoadNextPage` (lifted/shared from `OSSObjectList`) on scroll.

- **`OSSThumbnail.tsx` + `useLazyThumbnail.ts`** (`components/oss/`) — shared lazy image. `useLazyThumbnail({ ref, enabled, onEnter })` wires an `IntersectionObserver` on `ref` that fires `onEnter` once when the element first intersects (then disconnects). `OSSThumbnail` props: `objectKey`, `contentType`, `url?`, `onEnsure: () => void`, plus sizing/className. If `isImage(contentType)`: on first intersect call `onEnsure`; render `<img src={url}>` once `url` present, else a shimmer/icon placeholder; `onError` → fall back to the type-icon. Non-image → the type-icon immediately, no observer/presign. Used by both the detail preview and grid tiles.

- **`objectContentType.ts`** (`lib/`) — pure helper: `isImage(contentType: string): boolean` (`content-type` starts with `image/`, with an extension fallback for `.png/.jpg/.jpeg/.gif/.webp/.svg/.bmp/.avif`), and `typeIcon(contentType: string, key: string): LucideIcon` mapping common families (image/video/audio/text/json/archive/pdf/…) to a lucide icon, default `File`. Consumed by grid + detail + thumbnail.

### Additive to existing (no reshape; existing props/behavior/tests unchanged)

- **`OSSBreadcrumb.tsx`** — gains `viewMode?: "list"|"grid"` + `onViewModeChange?: (m) => void`, rendering a list/grid segmented toggle in the action area (next to upload/refresh) when both are provided.
- **`OSSObjectList.tsx`** — gains `focusedKey?: string | null` + `onFocusObject?: (key: string) => void`. Single-click an object row → `onFocusObject(o.key)`; the focused row gets a highlight class. Checkbox (`onToggleSelect`) and folder double-click (`onNavigatePrefix`) unchanged. **The checkbox cell must `stopPropagation` on click** so toggling multi-select does NOT also fire the row's focus handler (checkbox = select, row = focus, are independent). `shouldLoadNextPage` is exported for grid reuse (already exported in P3b-1).
- **`OSSBrowserPanel.tsx`** — renders `OSSObjectList` when `viewMode==="list"` else `OSSObjectGrid`; passes `viewMode`/`onViewModeChange` to the breadcrumb and `focusedKey`/`onFocusObject` to both; renders `OSSObjectDetail` as a right column when `focusedKey != null`; hosts `OSSPresignDialog` (opened from the detail's share button) and the single-object delete `ConfirmDialog`.

---

## §5. Data flows

1. **Focus → detail.** single-click row/tile → `focusObject(tabId, key)` → panel derives the focused `ObjectItem` by `key` from `state.listing.objects` → renders `OSSObjectDetail`. Close (`X`) → `focusObject(tabId, null)`. `navigateToPrefix`/`refresh` replacing the listing clears `focusedKey`.
2. **Thumbnail.** `OSSThumbnail` for an image tile/preview scrolls into view → `onEnsure` → `ensureThumbnail(tabId, key)` → (uncached) `OSSPresignGet(…expirySecs:0)` → `patch` `thumbnails[key]` → `<img src>` renders. Non-image → type-icon, no presign. Presign error → icon fallback, no toast.
3. **Share.** detail **生成分享链接** → panel opens `OSSPresignDialog` for `focusedKey` → user picks method+expiry → **生成/重新生成** → `OSSPresignGet`/`Put` → URL shown → **复制** (`notifyCopied`) / **复制并关闭**. Generate error → `toast.error`.
4. **Download.** detail **下载** → `startDownload(tabId, assetId, bucket, focusedKey)` (P3b-2 transfer store) → dock progress (existing).
5. **Delete (detail).** detail **删除** → single-object `ConfirmDialog` (`deleteConfirmOne`) → `deleteObject(tabId, focusedKey)` → `OSSRemoveObject` → refresh + clear focus; success → `notifySuccess`, failure → `toast.error`.
6. **View toggle.** breadcrumb toggle → `setViewMode(tabId, mode)` → panel swaps list/grid; `focusedKey` persists across the swap (the detail pane stays open on the same object).
7. **Grid pagination.** grid scroll-near-bottom → `loadNextPage(tabId)` (P3b-1, unchanged).

---

## §6. Layout

- **3-pane panel** (per `oss-object-detail`): `[bucket sidebar | resize | list/grid center | detail pane]`. The detail pane is a right column rendered only when `focusedKey != null`, **resizable** via `useResizeHandle` (like the sidebar): default 320, min 260, max 520. It has its own resize handle on its left edge and a close button.
- **Grid tiles:** fixed tile width ~150px, wrapping rows (CSS grid / flex-wrap — React handles wrapping; the pen's single-axis constraint is a pen-only limitation). Each tile: square-ish thumbnail/icon block on top, name (truncated) + size below. Focused tile = ring highlight.
- **View toggle:** a two-button segmented control (list icon / grid icon) in the breadcrumb action row.

---

## §7. Error / empty / loading

- **Thumbnail:** presign failure or `<img>` `onError` → silent type-icon fallback (§2.9). A loading image shows a shimmer/icon placeholder until `url` resolves.
- **Detail pane:** only rendered when a valid focused `ObjectItem` exists; if the focused key vanished after a refresh, `focusedKey` is cleared and the pane closes.
- **Share dialog:** `loading` disables generate/copy while a presign is in flight; empty `url` disables 复制/复制并关闭; generate failure → `toast.error(oss.share.generateFailed)`.
- **Grid:** reuses the list's loading skeleton / empty placeholder semantics (`loading` && no data → skeleton; `!loading` && empty → `oss.browser.empty`). Page-loading spinner at the bottom while `loadingPage`.
- **deleteObject:** failure sets store `error` + rethrows → panel `toast.error(oss.browser.deleteFailed)`.

---

## §8. Reuse map

- **Store:** extend `ossBrowserStore` (per-tab `patch`, `tabCloseHook`) — do not spin up a new store. `focusedKey` reset folds into the existing `navigateToPrefix`/`refresh`.
- **Transfer:** detail 下载 reuses `ossTransferStore.startDownload` (P3b-2).
- **UI primitives:** `@opskat/ui` `Dialog`/`Button`/`ScrollArea`/`ConfirmDialog`/`useResizeHandle`; `formatBytes` (`@/lib/formatBytes`); `notifyCopied`/`notifySuccess` (`@/lib/notify`); lucide icons.
- **Pagination:** `shouldLoadNextPage` shared between list and grid.
- **Patterns (reference, not copied):** `EtcdKeyDetail` meta-grid + copy-key layout; `useNativeFileDrop`/`useOssFileDrop` observer-hook shape for `useLazyThumbnail`.
- **i18n:** new namespaces `oss.detail.*`, `oss.share.*`, `oss.view.*` added up-front (en + zh-CN, lockstep); reuse existing `oss.browser.{empty,deleteConfirmOne,deleteConfirmTitle,deleteSuccess,deleteFailed}` and `action.{cancel,confirm}`.

### i18n keys (both locales)
- `oss.view.list`, `oss.view.grid` (toggle tooltips).
- `oss.detail.copyKey`, `oss.detail.share`, `oss.detail.download`, `oss.detail.delete`, `oss.detail.close`, `oss.detail.noPreview`, `oss.detail.size`, `oss.detail.type`, `oss.detail.etag`, `oss.detail.storageClass`, `oss.detail.lastModified`, `oss.detail.keyLabel`.
- `oss.share.title`, `oss.share.methodGet`, `oss.share.methodPut`, `oss.share.expiry15m`, `oss.share.expiry1h`, `oss.share.expiry24h`, `oss.share.expiry7d`, `oss.share.urlLabel`, `oss.share.generate`, `oss.share.regenerate`, `oss.share.close`, `oss.share.copyAndClose`, `oss.share.warning`, `oss.share.generateFailed`, `oss.share.copied`.

---

## §9. Testing strategy

- **Pure (`objectContentType.ts`):** `isImage` (content-type + extension fallback, negatives), `typeIcon` family mapping + default.
- **Store (`ossBrowserStore`):** `setViewMode`; `focusObject` open/close; `ensureThumbnail` presigns once + caches + no duplicate on cached/in-flight; `focusedKey` cleared on `navigateToPrefix`/`refresh`; `deleteObject` calls `OSSRemoveObject` + refresh + clears focus + rethrows on failure; two-tab isolation for the new fields.
- **Components:** `OSSObjectDetail` (metadata renders from `ObjectItem`; buttons fire callbacks; image→thumbnail vs non-image→icon); `OSSPresignDialog` (method/expiry state; generate calls the right binding with the right `expirySecs`; changing method clears url; copy fires `notifyCopied`; generate error toasts); `OSSObjectGrid` (folder vs object tiles; single-click→`onFocusObject`; double-click folder→navigate; focused highlight; lazy thumbnail via a **mocked `IntersectionObserver`**); `OSSThumbnail`/`useLazyThumbnail` (observer fires `onEnsure` once; `<img>` `onError`→icon; non-image skips presign); additive `OSSObjectList` focus prop; additive `OSSBreadcrumb` view toggle; panel wiring (toggle swaps list/grid, single-click opens detail, detail actions, dialog open).
- **i18n:** parity/coverage green with all new keys referenced (full-suite gate on the final task).
- **Deferred to manual / live-MinIO (per §7 — do NOT write DOM tests for these):** real presigned-URL validity + actual image rendering; real scroll/`IntersectionObserver` intersection in a browser; clipboard writes; the GET/PUT links actually working against a bucket. (Tests mock `IntersectionObserver` and the presign bindings.)

---

## §10. Concurrency / environment note

The working tree currently also holds **uncommitted etcd/i18n changes from a concurrent session** (an unrelated "KV Browse" feature) plus an untracked `ai-follow-terminal` spec. P3b-3 work must NOT touch, stage, or revert any of it — every commit stages only its own target files, and any spurious etcd/i18n test failure during a full-suite gate is external and out of scope. The `frontend/wailsjs/**` + `frontend/pnpm-lock.yaml`/`pnpm-workspace.yaml` env traps from P3b-1/P3b-2 still apply (generated/gitignored; revert lockfiles before commit; `git add` only target files).

---

## Task-group preview (for writing-plans; ~9–11 TDD tasks)

1. `objectContentType.ts` (`isImage`/`typeIcon`) + test.
2. i18n `oss.detail.*`/`oss.share.*`/`oss.view.*` (both locales).
3. `ossBrowserStore` extensions (`viewMode`/`focusedKey`/`thumbnails` + `setViewMode`/`focusObject`/`ensureThumbnail`/`deleteObject` + focus-clear folded into navigate/refresh) + tests.
4. `useLazyThumbnail` + `OSSThumbnail` + test.
5. `OSSObjectDetail` + test.
6. `OSSPresignDialog` + test.
7. `OSSObjectGrid` + test.
8. Additive props: `OSSObjectList` (`focusedKey`/`onFocusObject`) + `OSSBreadcrumb` (view toggle) + tests.
9. `OSSBrowserPanel` wiring (view swap, detail pane + resize, share dialog, single-object delete) + tests + full-suite gate.

(writing-plans will finalize exact task boundaries, code, and TDD steps.)
