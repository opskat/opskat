import { useState, useRef, useEffect, useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { createPortal } from "react-dom";
import { Loader2, Copy, ArrowUp, ArrowDown, ArrowUpDown } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { toast } from "sonner";

export interface CellEdit {
  rowIdx: number;
  col: string;
  value: unknown; // new value
}

interface QueryResultTableProps {
  columns: string[];
  rows: Record<string, unknown>[];
  loading?: boolean;
  error?: string;
  editable?: boolean;
  edits?: Map<string, unknown>; // key: "rowIdx:col"
  onCellEdit?: (edit: CellEdit) => void;
  showRowNumber?: boolean;
  rowNumberOffset?: number;
}

function cellKey(rowIdx: number, col: string) {
  return `${rowIdx}:${col}`;
}

type SortDir = "asc" | "desc" | null;

function compareValues(a: unknown, b: unknown): number {
  if (a == null && b == null) return 0;
  if (a == null) return -1;
  if (b == null) return 1;
  // Try numeric comparison
  const na = Number(a);
  const nb = Number(b);
  if (!isNaN(na) && !isNaN(nb)) return na - nb;
  return String(a).localeCompare(String(b));
}

const ROW_HEIGHT = 28;

export function QueryResultTable({
  columns,
  rows,
  loading,
  error,
  editable,
  edits,
  onCellEdit,
  showRowNumber,
  rowNumberOffset = 0,
}: QueryResultTableProps) {
  const { t } = useTranslation();

  const [editingCell, setEditingCell] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Sort state
  const [sortCol, setSortCol] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<SortDir>(null);

  // Context menu state
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; value: unknown } | null>(null);
  const ctxMenuRef = useRef<HTMLDivElement>(null);

  // Reset sort when columns change
  useEffect(() => {
    setSortCol(null);
    setSortDir(null);
  }, [columns]);

  // Sorted row indices (sort on the original indices to preserve edit mapping)
  const sortedIndices = useMemo(() => {
    const indices = rows.map((_, i) => i);
    if (!sortCol || !sortDir) return indices;
    return indices.sort((a, b) => {
      const cmp = compareValues(rows[a][sortCol], rows[b][sortCol]);
      return sortDir === "asc" ? cmp : -cmp;
    });
  }, [rows, sortCol, sortDir]);

  const toggleSort = useCallback(
    (col: string) => {
      if (sortCol !== col) {
        setSortCol(col);
        setSortDir("asc");
      } else if (sortDir === "asc") {
        setSortDir("desc");
      } else {
        setSortCol(null);
        setSortDir(null);
      }
    },
    [sortCol, sortDir]
  );

  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: sortedIndices.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 30,
  });

  // Focus input when editing starts
  useEffect(() => {
    if (editingCell && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [editingCell]);

  // Close context menu on outside click / escape
  useEffect(() => {
    if (!ctxMenu) return;
    const close = () => setCtxMenu(null);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    const onPointer = (e: PointerEvent) => {
      if (ctxMenuRef.current?.contains(e.target as Node)) return;
      close();
    };
    const timer = setTimeout(() => {
      document.addEventListener("pointerdown", onPointer, true);
    }, 50);
    document.addEventListener("keydown", onKey);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("pointerdown", onPointer, true);
      document.removeEventListener("keydown", onKey);
    };
  }, [ctxMenu]);

  const commitEdit = useCallback(
    (rowIdx: number, col: string, newValue: string) => {
      const original = rows[rowIdx]?.[col];
      const originalStr = original == null ? "" : String(original);
      if (newValue !== originalStr) {
        onCellEdit?.({
          rowIdx,
          col,
          value: newValue === "" && original == null ? null : newValue,
        });
      }
      setEditingCell(null);
    },
    [rows, onCellEdit]
  );

  const handleCopyCell = useCallback(() => {
    if (!ctxMenu) return;
    const text = ctxMenu.value == null ? "" : String(ctxMenu.value);
    navigator.clipboard.writeText(text);
    toast.success(t("query.copied"));
    setCtxMenu(null);
  }, [ctxMenu, t]);

  const handleCellContextMenu = useCallback((e: React.MouseEvent, value: unknown) => {
    e.preventDefault();
    e.stopPropagation();
    setCtxMenu({ x: e.clientX, y: e.clientY, value });
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (error) {
    return <div className="px-3 py-4 text-xs text-destructive whitespace-pre-wrap font-mono">{error}</div>;
  }

  if (columns.length === 0) {
    return (
      <div className="flex items-center justify-center py-8 text-xs text-muted-foreground">{t("query.noResult")}</div>
    );
  }

  return (
    <div className="flex-1 min-h-0 flex flex-col">
      {/* Sticky header */}
      <div className="shrink-0 overflow-hidden border-b border-border">
        <table className="w-full border-collapse text-xs font-mono table-fixed">
          <thead className="bg-muted">
            <tr>
              {showRowNumber && (
                <th className="border border-border px-2 py-1.5 text-center font-semibold text-muted-foreground whitespace-nowrap w-[50px]">
                  #
                </th>
              )}
              {columns.map((col) => {
                const isSorted = sortCol === col;
                return (
                  <th
                    key={col}
                    className="border border-border px-2 py-1.5 text-left font-semibold text-muted-foreground whitespace-nowrap cursor-pointer hover:bg-accent/50 select-none"
                    onClick={() => !editable && toggleSort(col)}
                    title={editable ? col : t("query.sortColumn")}
                  >
                    <span className="inline-flex items-center gap-1">
                      {col}
                      {!editable &&
                        (isSorted && sortDir === "asc" ? (
                          <ArrowUp className="h-3 w-3" />
                        ) : isSorted && sortDir === "desc" ? (
                          <ArrowDown className="h-3 w-3" />
                        ) : (
                          <ArrowUpDown className="h-3 w-3 opacity-0 group-hover:opacity-30" />
                        ))}
                    </span>
                  </th>
                );
              })}
            </tr>
          </thead>
        </table>
      </div>

      {/* Virtualized body */}
      <div ref={scrollRef} className="flex-1 overflow-auto min-h-0">
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          <table
            className="w-full border-collapse text-xs font-mono table-fixed"
            style={{ position: "absolute", top: 0, left: 0 }}
          >
            <tbody>
              {virtualizer.getVirtualItems().map((virtualRow) => {
                const origIdx = sortedIndices[virtualRow.index];
                const row = rows[origIdx];
                return (
                  <tr
                    key={virtualRow.key}
                    className={virtualRow.index % 2 === 0 ? "bg-background" : "bg-muted/40"}
                    style={{
                      position: "absolute",
                      top: virtualRow.start,
                      height: virtualRow.size,
                      width: "100%",
                      display: "table-row",
                    }}
                  >
                    {showRowNumber && (
                      <td className="border border-border px-2 py-1 text-center text-muted-foreground whitespace-nowrap w-[50px]">
                        {rowNumberOffset + origIdx + 1}
                      </td>
                    )}
                    {columns.map((col) => {
                      const ck = cellKey(origIdx, col);
                      const isEdited = edits?.has(ck);
                      const displayValue = isEdited ? edits!.get(ck) : row[col];
                      const isEditing = editingCell === ck;

                      return (
                        <td
                          key={col}
                          className={`border border-border px-2 py-1 whitespace-nowrap max-w-[400px] ${
                            isEdited ? "bg-yellow-100 dark:bg-yellow-900/30" : ""
                          }`}
                          title={displayValue == null ? "NULL" : String(displayValue)}
                          onDoubleClick={() => {
                            if (!editable) return;
                            setEditingCell(ck);
                          }}
                          onContextMenu={(e) => handleCellContextMenu(e, displayValue)}
                        >
                          {isEditing ? (
                            <input
                              ref={inputRef}
                              className="w-full bg-transparent outline-none border-none p-0 m-0 text-xs font-mono"
                              defaultValue={displayValue == null ? "" : String(displayValue)}
                              onBlur={(e) => commitEdit(origIdx, col, e.target.value)}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") {
                                  commitEdit(origIdx, col, (e.target as HTMLInputElement).value);
                                }
                                if (e.key === "Escape") {
                                  setEditingCell(null);
                                }
                              }}
                            />
                          ) : displayValue == null ? (
                            <span className="text-muted-foreground italic">NULL</span>
                          ) : (
                            <span className="truncate block max-w-[400px]">{String(displayValue)}</span>
                          )}
                        </td>
                      );
                    })}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      {/* Cell context menu */}
      {ctxMenu &&
        createPortal(
          <div
            ref={ctxMenuRef}
            className="z-50 min-w-[8rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md animate-in fade-in-0 zoom-in-95"
            style={{ position: "fixed", top: ctxMenu.y + 2, left: ctxMenu.x + 2 }}
          >
            <div
              role="menuitem"
              className="relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground"
              onClick={handleCopyCell}
            >
              <Copy className="h-3.5 w-3.5" />
              {t("query.copyValue")}
            </div>
          </div>,
          document.body
        )}
    </div>
  );
}
