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

export function DataTable({ onCopyAs, ...props }: HostDataTableProps) {
  return (
    <QueryResultTable
      {...props}
      copyAsSql={false}
      onCopyAs={
        onCopyAs
          ? (format, { rowIdx, col, value }) => onCopyAs(format as HostCopyFormat, { rowIdx, col, value })
          : undefined
      }
    />
  );
}
