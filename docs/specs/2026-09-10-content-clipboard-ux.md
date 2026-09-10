# Content clipboard and selection UX

> Status: Draft
> Owner: OpsKat maintainers
> Last updated: 2026-09-10

**Objective:** Make `Ctrl/Cmd+C` copy what the user has selected in the focused content surface — asset references only from the asset sidebar — and let the database result grid copy and paste rows with the keyboard.

**Hard invariant:** Except for the mis-captured keys named below, existing observable behaviour is unchanged: the asset-tree context menu still copies a reference; the grid context menu copy / copy-as / paste-cell / delete actions; the pending-edit → SQL-preview save flow; and the SFTP file manager's copy / cut / paste of files all keep their current result.

## Problem

1. **A global shortcut steals `Ctrl/Cmd+C` from every non-input surface.** `useKeyboardShortcuts` listens on `window` in the capture phase and, whenever an asset is selected, calls `shouldCopyAssetRef` (`frontend/src/hooks/useKeyboardShortcuts.ts:22`–`31`). That predicate (`frontend/src/lib/assetRef.ts:104`) only rules out inputs and a non-collapsed DOM selection, so a grid cell click — which produces a custom selection, not a DOM range — is read as "the user wants the asset reference". The reported symptom is a database user pressing `Ctrl/Cmd+C` on a selected cell and getting "Asset reference copied".
2. **The same capture makes the SFTP file manager's clipboard keys dead.** `FileManagerPanel` registers its own non-capturing `window` keydown for copy / cut / paste of files (`frontend/src/components/terminal/FileManagerPanel.tsx:704`–`733`), but the capturing handler runs first and calls `stopPropagation`, so the SFTP listener never sees the event while an asset is selected. This is a real bug, not only a wrong reference copied.
3. **The result grid has no keyboard copy.** `QueryResultTable` handles only arrow navigation, `Enter`/`F2` and `Escape` (`frontend/src/components/query/QueryResultTable.tsx:1159`). Copying a cell, a whole row, a column or several rows is reachable only through the row/cell context menu, even though the selection and the selection→TSV code already exist.
4. **The editable grid cannot paste rows.** The only paste is the single-cell context-menu action (`QueryResultTable.tsx:796`), which writes the clipboard verbatim into one cell. There is no way to copy a row and paste it as a row, or to paste several rows — not even onto a newly added blank row, which is the flow the report names.
5. **Lists that offer copy only in a menu.** The OSS object list has a multi-select (`selection`) but no keyboard copy; the Redis key list has a selected key and a "copy key name" menu item (`frontend/src/components/query/RedisKeyBrowser.tsx:441`, `:713`) but no keyboard copy. Both are currently stolen by problem 1.
6. **A lying success message.** `OSSObjectDetail` reports the copy of the object key with `notifyCopied(t("oss.detail.copyKey"))` (`frontend/src/components/oss/OSSObjectDetail.tsx:46`) — `oss.detail.copyKey` is the button label ("复制键名"), so the toast says "Copy key name" instead of "Copied".

## Actors and user stories

1. As a user working in any content surface, I want `Ctrl/Cmd+C` to copy what I selected there, so that list and grid selections are not silently replaced by an asset reference.
2. As a database user, I want to select a cell, a column, a row or several rows and copy them, so that moving data between environments needs no context-menu trip.
3. As a database user, I want to paste a copied row (or several rows) into a new row of an editable table, so that inserting repeated data does not mean retyping every column.
4. As a user browsing object storage or Redis keys, I want `Ctrl/Cmd+C` to copy the selected keys, so that I can paste them into a terminal or another tool.
5. As an SFTP user, I want `Ctrl/Cmd+C/X/V` to copy, cut and paste files again.

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | The asset-reference shortcut fires **only while the asset sidebar is the focused surface**. Focus is granted to the sidebar when the user clicks a non-interactive part of it (asset row, group row, empty area). | The shortcut's intent is "copy the asset I am pointing at"; binding it to the surface that owns asset selection removes the cross-surface conflict at the root. Rejected: keeping the global fallback and having each content surface opt out — unmarked surfaces stay broken, which is the bug being fixed. Rejected: matching the event target against a data attribute — it cannot distinguish "an asset is selected elsewhere" from "this surface is what the user is acting on". |
| 2 | Grid keyboard copy reuses the existing selection→TSV logic behind the context-menu copy, with precedence **row selection → column selection → focused cell**. | One code path means keyboard and menu copy cannot drift; the precedence matches the grid's existing mutual exclusion between row, column and cell selection. Rejected: a second, keyboard-specific serialiser — two sources of truth for the same action. |
| 3 | A DOM text selection inside the grid, or an active cell editor, is left to the browser. | Dragging across cells to copy literal text, or copying inside the editor's input, must keep working. Rejected: always overriding — it would break copying a partial cell value. |
| 4 | Grid keyboard paste parses clipboard text as tab-separated values with the project's existing parser, and writes the result as **pending edits** from an anchor cell. | Pending edits are the grid's established staging area; a paste therefore follows the same save and preview path as typing. The parser already exists in `frontend/src/lib/tableImport.ts`. Rejected: executing inserts directly — it would bypass the reviewed save flow. Rejected: comma detection — a single value containing commas must stay one cell when pasted. |
| 5 | Paste that runs past the last displayed row appends that many unsaved rows, the same state the "+ 新增行" affordance creates. | The report explicitly asks to paste a row into a new row; appending is the existing mechanism for a row that does not exist in the database yet. Rejected: refusing the overflow — it forces the user to add rows by hand before pasting. |
| 6 | Paste maps columns by their on-screen order from the anchor cell, and ignores cells beyond the last visible column. | It matches how copy writes the selection (visible columns in display order), so copy→paste round-trips inside the app. Rejected: matching by header name — the clipboard usually has no header row, and inventing one is a separate feature. |
| 7 | Read-only grids copy but never paste. | SQL results and MongoDB documents have no pending-edit path; a paste target that cannot be saved would be a dead end. |
| 8 | OSS and Redis list copy is bound to the platform copy key, not to a configurable shortcut. | It is the standard system copy key, and these lists have no shortcut entry today. Rejected: reusing `asset.copyRef` — it is a different action and the user may rebind it. |
| 9 | The SFTP file manager is not restructured; only its captured keys are freed. | Its handler already has the correct semantics and its own open/active guard; the defect is upstream. Rejected: migrating it to a container-scoped handler — a drive-by refactor outside this change. |

## Asset-reference shortcut scope

While the asset sidebar is the focused surface, the asset-reference shortcut (default `Ctrl/Cmd+C`, rebindable in Settings) copies the selected asset's Markdown reference and shows the existing confirmation, provided an asset is selected and there is no non-collapsed text selection. While any other surface is focused, the shortcut does nothing and the key reaches whatever owns the focus.

Clicking a non-interactive part of the sidebar — an asset row, a group row, or empty space — makes the sidebar the focused surface. Clicking a control inside it (the search field, the add buttons, a menu item) keeps that control's own behaviour; the search field keeps native copy. Taking this focus draws no focus ring, so selecting an asset looks unchanged.

Because the global capture no longer applies outside the sidebar, the SFTP file manager's copy, cut and paste of files respond again, and every other content surface keeps native copying.

## Result grid keyboard copy

With the result grid focused and no cell editor open, `Ctrl/Cmd+C`:

- copies every selected row across the visible columns, in current display order, when one or more rows are selected;
- otherwise copies every displayed row across the selected columns, in current display order, when one or more columns are selected;
- otherwise copies the focused cell's value;
- otherwise does nothing.

The clipboard receives tab-separated cells with newline-separated rows, identical to what the context-menu copy produces for the same selection. On success the user sees the existing "copied" confirmation. When text is selected in the page or a cell editor has focus, the key is left to the browser and no confirmation appears.

This applies to the editable table grid, the SQL result grid and the MongoDB result grid.

## Editable grid keyboard paste

With an editable grid focused, a cell editor closed and non-empty clipboard text, `Ctrl/Cmd+V` pastes a table of tab-separated values starting at the focused cell. When only rows are selected, the anchor is the first selected row's first visible column. With no cell and no row selected, nothing is pasted.

Each pasted value becomes a pending edit of the corresponding cell, exactly as if it had been typed. A single value pastes into the anchor cell. A paste whose rows or columns run past the end of the current page appends the missing rows as unsaved rows, so pasting a copied row into a newly added blank row fills that row, and pasting several rows adds as many rows as needed. Once editing is complete the user saves through the existing preview/confirm flow; no statement is executed by the paste itself.

Values falling beyond the last visible column are discarded. When the clipboard cannot be read or contains only whitespace, nothing changes and no error is shown. Read-only grids ignore the paste entirely.

## List keyboard copy

With the OSS object list focused and one or more objects selected, `Ctrl/Cmd+C` copies the selected object keys, one per line, and confirms. With nothing selected it copies the object that has focus.

With the Redis key list focused and a key selected, `Ctrl/Cmd+C` copies the key name and confirms.

Copying the object key from the OSS detail panel now reports success with a "copied" message instead of the button label.

## Consistency

- The grid context menu's copy item shows the copy key binding next to it, as the asset tree's reference item already does.
- The asset-reference entry in Settings states that it applies while the asset sidebar is focused.

## Out of scope

- Cell/row copy for the plain etcd and Kubernetes resource tables: they have no selection model, and adding one is a separate feature.
- Pasting from the clipboard into the import dialog, which today reads a file.
- CSV auto-detection, header-name column mapping, and rectangular (drag) selection in the grid.
- Restructuring the SFTP file manager's keyboard handling.

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `shouldCopyAssetRef` | Returns true only for a target inside the asset sidebar, with an asset selected and no text selection | `frontend/src/lib/assetRef.test.ts` |
| `useKeyboardShortcuts` | Copies the reference when the sidebar is focused; leaves the key untouched elsewhere, including a grid surface | `frontend/src/__tests__/useKeyboardShortcuts.terminal.test.tsx` |
| `QueryResultTable` keyboard copy | Cell / column / row / multi-row selections each write the expected TSV; text selection and editor focus defer to the browser | `frontend/src/__tests__/QueryResultTable.test.tsx` (clipboard mock, context-menu copy cases) |
| `TableDataTab` keyboard paste | A pasted block becomes pending edits; overflow appends unsaved rows; a read-only grid has no paste | `frontend/src/__tests__/TableDataTab.toolbar.test.tsx`, `tableSql.test.ts` |
| OSS / Redis list copy | The focused row's platform copy key writes the selected keys | `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx`, `frontend/src/__tests__/RedisKeyBrowser.test.tsx` |
| SFTP file manager | Copy with a file selected writes file paths, not an asset reference, while an asset is selected | `frontend/src/__tests__/FileManagerPanel.test.tsx` |

The interaction is also exercised in the sandbox against a real database table (select → copy → add row → paste), because jsdom cannot render the virtualised grid's scroll geometry.

## Open questions

None.
