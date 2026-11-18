import type { K8sListSort, K8sResourceRow, K8sTableColumn } from "@/stores/k8sStore";
import { labelSelectorOf } from "@/stores/k8sStore";

/** The server's age column is relative text ("5m", "2d"), so it sorts by the row's creation time instead. */
function isAgeColumn(column: K8sTableColumn) {
  return column.name.toLowerCase() === "age";
}

function cellText(cell: unknown): string {
  if (cell === null || cell === undefined) return "";
  return typeof cell === "object" ? JSON.stringify(cell) : String(cell);
}

export { cellText };

const CHAR_PX = 7.5; // text-xs mono/sans average, with headroom
const CELL_PAD_PX = 32; // pr-4 plus the status pill's own padding
const MIN_COL_PX = 72;

/** Display width in characters: CJK counts double. */
function textUnits(text: string): number {
  let n = 0;
  for (const ch of text) n += ch.charCodeAt(0) > 0x2e80 ? 2 : 1;
  return n;
}

/** Pixel width per column so that the header and every value of every row fits without truncation. */
export function columnWidths(columns: { name: string }[], rows: Pick<K8sResourceRow, "cells">[]): number[] {
  return columns.map((col, i) => {
    // The header leaves room for the sort arrow.
    let units = textUnits(col.name) + 2;
    for (const row of rows) units = Math.max(units, textUnits(cellText(row.cells[i])));
    return Math.max(MIN_COL_PX, Math.ceil(units * CHAR_PX) + CELL_PAD_PX);
  });
}

/** Name filter (a filter holding `=` is a label selector the cluster already applied) then sort. */
export function visibleRows(
  columns: K8sTableColumn[],
  rows: K8sResourceRow[],
  filter: string,
  sort: K8sListSort | null
): K8sResourceRow[] {
  const needle = labelSelectorOf(filter) ? "" : filter.trim().toLowerCase();
  const filtered = needle ? rows.filter((r) => r.name.toLowerCase().includes(needle)) : rows;

  if (columns.length === 0) return filtered;
  // A sort on a column that is no longer listed (the Namespace column after picking one namespace) is name ascending.
  const sorted = sort ? columns.findIndex((c) => c.name === sort.column) : -1;
  const index = Math.max(sorted, 0);
  const byTime = isAgeColumn(columns[index]);
  const sign = sorted >= 0 && sort?.dir === "desc" ? -1 : 1;
  return [...filtered].sort((a, b) => {
    if (byTime) return sign * (Date.parse(a.creationTimestamp) - Date.parse(b.creationTimestamp));
    return (
      sign *
      cellText(a.cells[index]).localeCompare(cellText(b.cells[index]), undefined, {
        numeric: true,
        sensitivity: "base",
      })
    );
  });
}
