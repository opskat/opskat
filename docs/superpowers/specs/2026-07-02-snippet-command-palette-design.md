# Design — Code snippets in the Ctrl+P command palette (#202)

## Problem

Running a code snippet today requires the mouse: open the Snippets page (or a
per-tool `SnippetPopover`), find the snippet, pick a host in `SnippetAssetDrawer`,
click Run. Issue #202 asks for a keyboard-first path. Two options were proposed
(per-snippet custom shortcuts + host binding, or a Ctrl+P dropdown). The
maintainer chose **Ctrl+P** and the reporter agreed.

## Key finding: reuse, don't build

A command palette already exists and is already bound to **⌘P / Ctrl+P**
(`command.quickopen` in `shortcutStore.ts`). `CommandPalette.tsx` renders a
fuzzy/pinyin-searchable dropdown listing **open tabs + recent/matching assets**.
This feature **adds a Snippets section to that existing palette** — it does not
build a new launcher.

All backend IPC and run primitives already exist; **this is a frontend-only
change**:
- `ListSnippets`, `RecordSnippetUse`, `SetSnippetLastAssets`, `GetSnippetLastAssets` (bindings).
- `runSnippetOnAsset(asset, content)` — opens/reuses the right tool tab and
  inserts content, **never auto-executing**. For SSH it reuses an already
  connected pane via `findExistingConnectedPane`.
- `SnippetAssetDrawer` — the full pick-host → run → `SetLastAssets` →
  `RecordUse` flow.

## Scope

**In scope**
- New **Snippets** section in `CommandPalette`.
- Pick a snippet → resolve target → insert its content (never execute).
- Only **runnable** snippets appear (see runnable set below).

**Out of scope** (dropped from the original proposal / YAGNI)
- Per-snippet custom keyboard shortcuts.
- Static snippet→host binding.
- Extending run support to redis / k8s / prompt categories (they are not
  runnable via `runSnippetOnAsset` anywhere today).
- Auto-execute (Shift+Enter = insert+run) — noted as a future enhancement.
- Backend changes.

## Runnable set (mirror the Snippets page)

`SnippetsPage.tsx` gates its Run button on
`RUNNABLE_ASSET_TYPES = {ssh, database, mongodb}`. The palette mirrors this
exactly: only snippets whose category `assetType` is in that set are listed.
This naturally excludes `prompt` (assetType `""`) and `redis`/`k8s`, and avoids
the `runSnippetOnAsset` "unsupported asset type" throw.

To avoid a second copy of this rule, the definition is **extracted** from
`SnippetsPage.tsx` into `snippetRun.ts` and both consumers import it:
- `snippetRun.ts` exports `RUNNABLE_ASSET_TYPES` and
  `isRunnableCategoryId(categoryId, categories): boolean`.
- `SnippetsPage.tsx` drops its local `RUNNABLE_ASSET_TYPES`/`isRunnable` and
  imports the shared helper (in-scope refactor of the producer).

## Behavior (approved decisions)

1. **Placement:** extend the existing unified ⌘P palette.
2. **Target resolution — active tab first, else pick host.**
3. **Insert only** — never auto-execute (matches every other snippet path).

### Target resolution

A pure helper `resolveSnippetTarget({ snippet, activeTab, assetsById, categories })`
in `frontend/src/lib/snippetTarget.ts` returns one of:

- `{ kind: "active", asset }` — the active tab matches the snippet's category
  asset type, and that tab's asset is known:
  - terminal tab ⇒ matches an `ssh` snippet;
  - query tab ⇒ matches when `activeTab.meta.assetType === category.assetType`
    (`database` / `mongodb`).
- `{ kind: "pick" }` — no active-tab match (or the asset can't be resolved from
  the store).

On activation the palette does:
- **active** → `runSnippetOnAsset(asset, snippet.Content)` then `recordUse(id)` +
  `setLastAssets(id, [asset.ID])`; close palette. (For SSH this reuses the
  connected pane; for query it opens/reuses the query editor — all insert-only.)
- **pick** → `useSnippetStore.getState().requestHostPick(snippet)`; close palette.
  App renders `SnippetAssetDrawer` for the pending snippet (pre-filled with
  last-used assets), reusing the existing run flow.

No `terminalStore` refactor is needed: `runSnippetOnAsset` already targets the
active connected SSH pane, so the "active terminal" case is covered by reuse.

## Components & data flow

```
Ctrl+P ──> CommandPalette
             ├─ reads snippetStore.all (runnable-filtered) + categories
             ├─ Snippets section:
             │    empty query  → top ~5 (already ordered by recency/use)
             │    typed query  → pinyinMatch(name|description)
             └─ activate(snippet):
                  resolveSnippetTarget(...)
                    active → runSnippetOnAsset + recordUse + setLastAssets
                    pick   → snippetStore.requestHostPick(snippet)
                  onClose()

App (top level)
  └─ {snippetStore.runTarget && <SnippetAssetDrawer snippet=runTarget onClose=clearHostPick/>}
```

### `snippetStore` additions
- `all: Snippet[]` and `loadAll()` — fetch all snippets (empty filter, default
  recency order) into a **separate** field so the Snippets-page `list`/`filter`
  is untouched. Guarded by a monotonic request id like `loadList`.
- `runTarget: Snippet | null`, `requestHostPick(snippet)`, `clearHostPick()` —
  drives the app-level drawer for the palette's pick path. (Adding transient
  snippet-run UI state to the snippet domain store is consistent with it already
  holding `filter`.)

### `CommandPalette` additions
- On open: `loadCategories()` + `loadAll()` (categories are needed for the
  runnable filter and the resolver; freshness matters for newly created snippets).
- New `SnippetRow` kind; rows render category badge + `FileCode` icon +
  highlighted name + description preview, consistent with existing rows.
- Snippets section placed after Opened / Assets (or Recent on empty query).
- Placeholder copy updated to mention snippets.

## Error handling
- `resolveSnippetTarget` never throws; unknown asset ⇒ `pick`.
- The active-run path relies on `runSnippetOnAsset`, which already throws only
  for unsupported types — impossible here since the list is runnable-filtered.
- `recordUse` is fire-and-forget (existing behavior). `setLastAssets` failures
  surface as before (drawer path) or are best-effort on the active path.

## Testing (TDD, vitest)
- `snippetTarget.test.ts` (new): terminal+ssh → active; query+database → active;
  query assetType mismatch → pick; active asset missing from store → pick; no
  active tab → pick.
- `snippetRun` runnable helper: true for ssh/database/mongodb; false for
  redis/k8s/prompt/unknown.
- `commandPalette.test.tsx` (extend): only runnable snippets listed; empty query
  shows recent snippets capped at ~5; typed query matches by name (incl. pinyin);
  Enter on an active-match calls `runSnippetOnAsset` (mocked) and not the drawer;
  Enter on a no-match calls `requestHostPick`.

## Files touched
- `frontend/src/components/snippet/snippetRun.ts` — export runnable helper.
- `frontend/src/components/snippet/SnippetsPage.tsx` — use shared helper.
- `frontend/src/lib/snippetTarget.ts` — **new** resolver.
- `frontend/src/stores/snippetStore.ts` — `all`/`loadAll`, `runTarget`/`requestHostPick`/`clearHostPick`.
- `frontend/src/components/command/CommandPalette.tsx` — Snippets section + activation.
- `frontend/src/App.tsx` — app-level `SnippetAssetDrawer` from `snippetStore.runTarget`.
- `frontend/src/i18n/locales/{en,zh-CN}/common.json` — `commandPalette.section.snippets`, placeholder copy.
- Tests as above.
