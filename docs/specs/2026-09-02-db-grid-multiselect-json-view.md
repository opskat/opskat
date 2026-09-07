# Database result multi-selection and JSON views

> Status: Draft
> Owner: OpsKat maintainers
> Last updated: 2026-09-02

**Objective:** Let a database user select multiple result rows and multiple tree tables, act on the whole selection, and read query results as JSON in addition to the grid.

**Hard invariant:** Frozen columns, column multi-selection, cell editing and single-row deletion keep their current observable behaviour; no JSON surface can write to the database.

## Problem

1. **Row multi-selection is implemented but unreachable.** `QueryResultTable` holds `selectedRowIdxs` (`frontend/src/components/query/QueryResultTable.tsx:363`), but every one of the eight writes to it clears the set (lines 411, 422, 434, 948, 988, 1004, 1030, 1069) — nothing ever adds a row. The `RowContextMenu` variant is declared (`:144`) with no producer, and the row branches of `handleCopyCell` (`:713`–`:745`) and `handleCopyAs` (`:801`–`:826`) plus the `onSelectedRowsChange` callback are therefore dead. `TableDataTab` already consumes `ctx.selectedRowIndices` when copying (`frontend/src/components/query/TableDataTab.tsx:784`), so a user can never trigger a multi-row copy.
2. **The row-number gutter was removed as collateral, not by decision.** `showRowNumber` and `rowNumberOffset` are still declared props (`QueryResultTable.tsx:90`–`:91`) and are passed by `TableDataTab` (`:1084`), `SqlEditorTab` (`:556`) and `MongoDBResultView` (`:131`), but nothing reads them. Commit `8e64ee1e` deleted the header cell and the `{rowNumberOffset + origIdx + 1}` body cell in the same change that rewrote frozen columns to "reorder to the left edge plus `left: frozenLeft`"; that commit's message carries six separate frozen-column positioning fixes and records no rationale for dropping the gutter. The test `does not render the row number column even when requested` (`frontend/src/__tests__/QueryResultTable.test.tsx:294`) pins the resulting state rather than a product requirement.
3. **Deletion cannot follow a multi-row selection.** `handleDeleteRow` takes one index and stores a single statement (`TableDataTab.tsx:723`–`:741`), which `SqlPreviewDialog` receives as a one-element array (`:1218`) even though its contract is `statements: string[]` (`frontend/src/components/query/SqlPreviewDialog.tsx:21`).
4. **JSON reading exists only for MongoDB.** `MongoDBResultView` has a `table | json` mode (`frontend/src/components/query/MongoDBResultView.tsx:24`) rendering `CodeEditor … language="json" readOnly` (`:138`). `TableDataTab` and `SqlEditorTab` render the grid only, so a row with many columns or long text values can only be read by horizontal scrolling.
5. **Tree tables are single-select.** `DatabaseTree` holds `selected: { db, table } | null` (`frontend/src/components/query/DatabaseTree.tsx:133`) and its context menu acts on exactly the right-clicked table (`:213`–`:275`), so dropping or truncating several tables requires repeating the confirm dialog once per table.

## Actors and user stories

1. As a database user, I want to select several result rows and copy them as INSERT/UPDATE/TSV in one action, so that I can move a subset of rows between environments without repeating a per-row copy.
2. As a database user, I want to delete several selected rows after reviewing the generated SQL, so that cleaning up a handful of records is one reviewed action rather than several.
3. As a database user, I want to read a result page as JSON, so that nested or long values are legible without horizontal scrolling.
4. As a database user, I want a JSON detail view of the row I am on, so that I can inspect a wide row without losing the grid.
5. As a database user, I want to select several tables in the tree and open, truncate or drop them together, so that routine multi-table maintenance is one confirmed action.

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | Reinstate a sticky row-number gutter as the selection affordance, reusing the existing `showRowNumber` / `rowNumberOffset` props and their existing call sites. | The props, the callers and the entire row-selection and row-context-menu machinery already exist; only the rendered gutter is missing. Rejected: a checkbox column — it adds a second selection idiom next to the click/Ctrl/Shift model that column selection already uses, and it needs a new prop. |
| 2 | Offset every frozen column's sticky `left` by the gutter width. | `frozenColumnOffsets` accumulates from zero (`QueryResultTable.tsx:494`–`:503`), so a gutter at `left: 0` would otherwise be overlapped by the first frozen column. Rejected: rendering the gutter as an ordinary non-sticky first column — it would scroll out of view, and row numbers must stay visible to remain a selection target. |
| 3 | Row selection reuses the anchor model of `selectColumn` (plain click sets, Ctrl/Cmd toggles, Shift extends from the anchor), and stays mutually exclusive with cell and column selection. | Users get one selection idiom across rows and columns, and the existing mutual-exclusion clearing already encodes that intent. Rejected: allowing simultaneous row and column selection — the copy paths at `:713` and `:801` branch on exactly one of the two being active. |
| 4 | Multi-row deletion builds one statement per row and executes them sequentially, aggregating affected/zero-affected/error counts. | `buildDeleteStatement` is per-row and already tested (`frontend/src/__tests__/tableSql.test.ts`), `SqlPreviewDialog` already accepts a statement array, and `handleSubmit` (`TableDataTab.tsx:534`–`:575`) established the sequential-execution and aggregation pattern. Rejected: one `DELETE … WHERE pk IN (…)` — it does not express composite or absent primary keys, which `buildDeleteStatement` handles today by matching on all columns. |
| 5 | JSON surfaces are read-only everywhere; editing stays in the grid. | Editing already has a reviewed edit-then-preview-SQL path; a writable JSON editor would need a second value-coercion and statement-building path. Rejected: an editable JSON row form — out of proportion to the request and duplicative of the cell editor. |
| 6 | The whole-result JSON view of an editable grid shows values with pending edits and pending new rows applied. | The two views must not disagree about what the user is looking at. Rejected: rendering server data with a "does not include pending changes" notice — it makes the JSON view untrustworthy exactly when edits are in flight. |
| 7 | JSON surfaces serialise untruncated values, unlike grid cells. | The grid caps rendered cell text at `CELL_DISPLAY_MAX_CHARS` for layout cost; reading a long value is the motivating case for the JSON views, so inheriting that cap would defeat them. Rejected: reusing `cellValueToDisplayText` for consistency — it would make both JSON surfaces useless for the values they exist to show. |
| 8 | The row detail panel sits to the right of the grid, is collapsed by default and its open state is remembered per tab. | Wide rows and long text are the motivating cases, which suit a vertical panel; defaulting to closed preserves the current layout and grid width. Rejected: auto-opening on cell selection — it narrows the grid whenever a user merely clicks a cell. |
| 9 | Extract one view-mode toggle component under `frontend/src/components/query/` and move `MongoDBResultView` onto it. | Two new call sites plus the hand-rolled toggle at `MongoDBResultView.tsx:101`–`:119` would otherwise leave three copies. The shared component wraps the design system's `Segmented` (`frontend/src/components/asset/fields.tsx:57`) rather than re-implementing it: `Segmented` merges an incoming `className`, and `VNCToolbar.tsx:80` / `RDPChrome.tsx:68` already shrink it to toolbar size that way. Rejected: copying the MongoDB markup into both SQL views; also rejected, a hand-rolled toggle — it duplicates `Segmented` and drops its dark-theme variants, under which `--background` is darker than `--muted` and the raised pill reads as recessed. |
| 10 | Tree table multi-selection may span databases, and a context menu opened on a selected table acts on the whole selection while one opened elsewhere resets to that single table. | Each executed statement already carries its own `database` argument, so cross-database batches need no extra machinery, and the "right-click inside the selection keeps it" rule matches `handleCellContextMenu` (`QueryResultTable.tsx:997`). Rejected: restricting selection to one database — it would require extra code to enforce a limit users do not benefit from. |

## Result grid row selection

The grid gains a leading row-number gutter whenever `showRowNumber` is set, showing `rowNumberOffset + n` so numbers stay continuous across server-side pages, with a corner cell in the header row.

Clicking a row number selects that row and clears any cell or column selection. Ctrl/Cmd-clicking toggles one row and moves the anchor to it. Shift-clicking selects the inclusive range between the anchor and the clicked row in current display order, so the range follows the active sort rather than the underlying row order. Clicking the header corner cell selects every row on the page when any row is unselected, and clears the selection when all are already selected. Escape clears a row selection, as it already does for column selection. Changing columns, changing the row set (paging, refresh, filter apply) or focusing a cell from outside clears the row selection, matching the existing clearing points.

Selected rows are visually marked by the same emphasis already applied to cells of selected rows, and the gutter cell of a selected row carries that emphasis too. Frozen columns remain sticky and correctly positioned to the right of the gutter under every combination of selection, editing and horizontal scroll.

Right-clicking a row number opens the row context menu. When the right-clicked row is part of the current selection, the menu's copy, copy-as and delete actions apply to the whole selection in display order; when it is not, the selection resets to that single row first. The menu offers copy, the existing copy-as formats, and delete.

## Multi-row deletion

Deleting a row selection in an editable grid produces one delete statement per selected persisted row and presents all of them in the existing SQL preview dialog before anything executes, retaining the current warning shown when the table has no primary key and statements therefore match on all columns.

Confirming executes the statements in order. The user is told the total number of affected rows on success; if some statements affect no rows the user is warned with that count, and if some fail the errors are surfaced. The grid refreshes and pending edits are discarded once at least one statement succeeded, matching the current single-row behaviour. Cancelling executes nothing.

Selected rows that are unsaved new rows are removed locally and contribute no statement. A selection containing only unsaved new rows therefore removes them without opening the preview dialog.

## Table and JSON views

Both the table-data grid and the SQL result grid gain a `table | json` toggle. The mode is per tab, defaults to `table`, and is remembered for the lifetime of the tab.

In JSON mode the current page's rows render as a read-only, indented JSON array of objects keyed by the visible column names. For the editable grid this includes pending cell edits and pending new rows, so the JSON always matches what the grid shows. JSON surfaces always serialise the untruncated value. This is deliberate: the grid truncates a cell's rendered text to `CELL_DISPLAY_MAX_CHARS` (`frontend/src/lib/cellValue.ts`) for layout cost, and reading a long value is exactly why a user switches to JSON — a JSON view that inherited the grid's truncation would not solve the problem it exists for. Values that are not JSON-representable fall back to the same textual rule `cellValueToText` already applies, again untruncated. An empty result renders as an empty array; a query error keeps showing the error rather than a JSON body.

Independently of the mode toggle, both grids gain a row detail panel to the right of the grid, collapsed by default, toggled from the toolbar and remembered per tab. When open it shows the current row — the row of the selected cell, or the single selected row — as read-only indented JSON, including pending edits for the editable grid. With no current row it shows an empty state. Paging or refreshing the grid clears the current row and returns the panel to its empty state without closing it.

Toggling views never issues a query, never changes paging and never discards pending edits.

## Tree table selection

Table nodes in the database tree support the same click, Ctrl/Cmd-click and Shift-click model as grid rows, with Shift extending over the currently visible, filtered table nodes. Selection may span databases and schemas. Every selected node is visually marked. Changing the tree filter, collapsing a database or refreshing its tables drops the affected nodes from the selection; dropping a table removes it from the selection.

Right-clicking a selected table applies open, truncate and drop to the entire selection; right-clicking an unselected table replaces the selection with that table first. Actions that are meaningful for one table only — view structure, alter table, new SQL — are disabled while more than one table is selected.

Opening a multi-table selection opens one table tab per selected table. Truncating or dropping a multi-table selection opens the existing confirmation, which lists every affected table qualified by its database, and executes one statement per table against that table's own database. The user is told how many tables succeeded, and any failures are surfaced without aborting the remaining tables. Dropped tables are removed from the tree by refreshing each affected database once.

## Out of scope

- Cell-range (Excel-style rectangular) selection.
- Exporting or importing across several tables at once; the export dialog stays bound to one loaded table's rows, columns and paging state.
- Editing through any JSON surface.
- Multi-selection in the Redis, etcd, Kafka, OSS and Kubernetes trees and result views.
- Batch operations on database or schema nodes in the tree.

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| `QueryResultTable` rendered output | Gutter renders with the right offsets when requested; click / Ctrl-click / Shift-click produce the expected selected row set; corner cell selects and clears all; Escape and row-set changes clear it | `frontend/src/__tests__/QueryResultTable.test.tsx` column-selection cases (`:172`, `:181`) |
| `QueryResultTable` frozen-column geometry | Frozen columns stay sticky and correctly offset to the right of the gutter, including when a row is selected | `QueryResultTable.test.tsx:265`, `:304`, `:321`, `:348`, `:391` |
| `QueryResultTable` row context menu | Right-clicking inside the selection reports every selected row to `onCopyAs`; right-clicking outside it resets to a single row | `QueryResultTable.test.tsx:235`, `:422` |
| `TableDataTab` deletion flow | A multi-row selection previews one statement per persisted row, executes them in order, aggregates the result, and removes unsaved new rows without a statement | `frontend/src/__tests__/TableDataTab.toolbar.test.tsx`, `frontend/src/__tests__/tableSql.test.ts` |
| JSON view rendering | JSON mode reflects pending edits and new rows; a value longer than `CELL_DISPLAY_MAX_CHARS` appears in full; the detail panel follows the current row and empties on page change | `frontend/src/__tests__/QueryResultTable.test.tsx` cell-value cases |
| `DatabaseTree` selection and batch actions | Ctrl/Shift selection across databases; context menu scope rule; single-table actions disabled on multi-selection; batch drop issues one statement per table against its own database | `frontend/src/__tests__/DatabaseTree.test.tsx` |

The pinning test `does not render the row number column even when requested` (`QueryResultTable.test.tsx:294`) contradicts requirement 1 of this spec and is replaced by the gutter rendering cases above.

Sticky positioning under real horizontal scrolling is not reliably observable in jsdom; the frozen-column geometry tests cover computed offsets and classes, and a runtime check in the sandbox against a wide table with a frozen column covers the rendered result.

## Open questions

None.
