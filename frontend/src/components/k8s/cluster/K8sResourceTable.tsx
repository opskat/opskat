import { cn } from "@opskat/ui";
import type { K8sResourceRow, K8sTableColumn } from "@/stores/k8sStore";

interface Props {
  columns: K8sTableColumn[];
  rows: K8sResourceRow[];
  /** Opens a row; rows aren't clickable without it. */
  onOpen?: (row: K8sResourceRow) => void;
}

function cellText(cell: unknown): string {
  if (cell === null || cell === undefined) return "";
  return typeof cell === "object" ? JSON.stringify(cell) : String(cell);
}

/**
 * The cluster's server-side Table as returned by ListK8sResources: the name column first, the namespace
 * column when listing across namespaces, then `kubectl get`'s default columns.
 */
export function K8sResourceTable({ columns, rows, onOpen }: Props) {
  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-background">
        <tr className="border-b text-muted-foreground">
          {columns.map((col, i) => (
            <th key={col.name} className={cn("whitespace-nowrap py-2 pr-4 text-left font-medium", i === 0 && "pl-3")}>
              {col.name}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr
            key={`${row.namespace}/${row.name}`}
            className={cn("border-b", onOpen && "cursor-pointer hover:bg-muted/40")}
            onClick={onOpen ? () => onOpen(row) : undefined}
          >
            {row.cells.map((cell, i) => (
              <td key={i} className={cn("whitespace-nowrap py-1.5 pr-4", i === 0 && "pl-3 font-mono font-medium")}>
                {cellText(cell)}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
