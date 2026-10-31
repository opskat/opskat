import { QueryResultTable } from "../../components/query/QueryResultTable";

export type HostRowDensity = "compact" | "default" | "comfortable";
export type HostSortDir = "asc" | "desc" | null;
/** Copy-as formats a generic table has; SQL statements are not among them. */
export type HostCopyFormat = "tsv-data" | "tsv-fields" | "tsv-fields-data";

export interface HostDataTableProps {
  columns: string[];
  rows: Record<string, unknown>[];
  loading?: boolean;
  showRowNumber?: boolean;
  rowNumberOffset?: number;
  rowDensity?: HostRowDensity;
  /** Sorting is controlled by the page when onSortChange is given, local otherwise. */
  sortColumn?: string | null;
  sortDir?: HostSortDir;
  onSortChange?: (column: string | null, dir: HostSortDir) => void;
  /** Adds a "copy as" submenu to the context menu; the page performs the copy. */
  onCopyAs?: (format: HostCopyFormat, ctx: { rowIdx: number; col: string; value: unknown }) => void;
  onSelectedCellChange?: (cell: { rowIdx: number; col: string } | null) => void;
  onSelectedRowsChange?: (rowIdxs: number[]) => void;
}

// Only the contract's props reach the internal table: a page is plain JS, and a
// spread would hand it every prop the query panel's table has.
export function DataTable({
  columns,
  rows,
  loading,
  showRowNumber,
  rowNumberOffset,
  rowDensity,
  sortColumn,
  sortDir,
  onSortChange,
  onCopyAs,
  onSelectedCellChange,
  onSelectedRowsChange,
}: HostDataTableProps) {
  return (
    <QueryResultTable
      columns={columns}
      rows={rows}
      loading={loading}
      showRowNumber={showRowNumber}
      rowNumberOffset={rowNumberOffset}
      rowDensity={rowDensity}
      sortColumn={sortColumn}
      sortDir={sortDir}
      onSortChange={onSortChange}
      onSelectedCellChange={onSelectedCellChange}
      onSelectedRowsChange={onSelectedRowsChange}
      copyAsSql={false}
      onCopyAs={
        onCopyAs
          ? (format, { rowIdx, col, value }) => onCopyAs(format as HostCopyFormat, { rowIdx, col, value })
          : undefined
      }
    />
  );
}
