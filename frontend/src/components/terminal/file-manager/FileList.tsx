import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { File, Folder, Loader2 } from "lucide-react";
import { Button, cn, Input, ScrollArea } from "@opskat/ui";
import { sftp_svc } from "../../../../wailsjs/go/models";
import {
  canMovePathToDirectory,
  formatBytes,
  formatDate,
  getEntryPath,
  getParentPath,
  splitNameForRename,
  sortEntries,
} from "./utils";

interface RenameInputProps {
  initialName: string;
  onCommit: (name: string) => void;
  onCancel: () => void;
}

function RenameInput({ initialName, onCommit, onCancel }: RenameInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState(initialName);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    const range = splitNameForRename(initialName);
    input.focus();
    input.setSelectionRange(0, range.stemLength);
  }, [initialName]);

  return (
    <Input
      ref={inputRef}
      value={value}
      className="h-5 flex-1 border-0 bg-background px-1 text-xs shadow-none focus-visible:ring-1"
      onChange={(e) => setValue(e.target.value)}
      onBlur={onCancel}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Enter") onCommit(value);
        if (e.key === "Escape") onCancel();
      }}
    />
  );
}

interface FileListProps {
  canExternalEdit?: (entry: sftp_svc.FileEntry) => boolean;
  clipboardCutPaths: Set<string>;
  currentPath: string;
  entries: sftp_svc.FileEntry[];
  error: string | null;
  loading: boolean;
  onExternalOpen?: (path: string) => void;
  onGoUp: () => void;
  onMoveEntriesToDirectory: (sourcePaths: string[], targetDirPath: string) => void;
  onNavigate: (path: string) => void;
  onOpenContextMenu: (x: number, y: number, entry: sftp_svc.FileEntry | null) => void;
  onRenameCancel: () => void;
  onRenameCommit: (oldPath: string, nextName: string) => void;
  onRetry: () => void;
  renamePath: string | null;
  selected: string[];
  setSelected: (next: string[] | ((prev: string[]) => string[])) => void;
}

export function FileList({
  canExternalEdit,
  clipboardCutPaths,
  currentPath,
  entries,
  error,
  loading,
  onExternalOpen,
  onGoUp,
  onMoveEntriesToDirectory,
  onNavigate,
  onOpenContextMenu,
  onRenameCancel,
  onRenameCommit,
  onRetry,
  renamePath,
  selected,
  setSelected,
}: FileListProps) {
  const { t } = useTranslation();
  const sortedEntries = useMemo(() => sortEntries(entries), [entries]);
  const entryPaths = useMemo(
    () => sortedEntries.map((entry) => getEntryPath(currentPath, entry)),
    [currentPath, sortedEntries]
  );
  const lastClickedRef = useRef<number | null>(null);
  const draggedPathsRef = useRef<string[]>([]);
  const pointerDragRef = useRef<{
    dragging: boolean;
    pointerId: number;
    sourcePath: string;
    sourcePaths: string[];
    startX: number;
    startY: number;
  } | null>(null);
  const suppressNextClickRef = useRef(false);
  const [dropTargetPath, setDropTargetPath] = useState<string | null>(null);
  const slowClickRef = useRef<{ path: string; time: number; timer: number | null }>({ path: "", time: 0, timer: null });

  const selectEntry = (path: string, index: number, event: React.MouseEvent) => {
    if (event.shiftKey && lastClickedRef.current !== null) {
      const start = Math.min(lastClickedRef.current, index);
      const end = Math.max(lastClickedRef.current, index);
      setSelected(entryPaths.slice(start, end + 1));
      return;
    }
    if (event.ctrlKey || event.metaKey) {
      setSelected((prev) => (prev.includes(path) ? prev.filter((item) => item !== path) : [...prev, path]));
      lastClickedRef.current = index;
      return;
    }
    setSelected([path]);
    lastClickedRef.current = index;
  };

  const maybeStartSlowRename = (path: string, index: number, eventTime: number) => {
    const now = eventTime;
    const prev = slowClickRef.current;
    if (prev.timer) window.clearTimeout(prev.timer);
    if (
      prev.path === path &&
      now - prev.time > 450 &&
      now - prev.time < 1400 &&
      selected.length === 1 &&
      selected[0] === path
    ) {
      prev.path = "";
      prev.time = 0;
      onOpenContextMenu(-1, -1, null); // closes any pending menu in parent no-op path
      window.dispatchEvent(new CustomEvent("sftp:rename-request", { detail: { path } }));
      return;
    }
    slowClickRef.current = { path, time: now, timer: null };
    slowClickRef.current.timer = window.setTimeout(() => {
      if (slowClickRef.current.path === path) slowClickRef.current.path = "";
    }, 1500);
    lastClickedRef.current = index;
  };

  const commitRename = (nextName: string) => {
    if (!renamePath) return;
    const trimmed = nextName.trim();
    if (!trimmed) {
      onRenameCancel();
      return;
    }
    onRenameCommit(renamePath, trimmed);
  };

  const isEntryTarget = (target: EventTarget | null) => {
    return target instanceof Element && !!target.closest("[data-sftp-entry-row]");
  };

  const getDragPaths = (event: React.DragEvent) => {
    if (draggedPathsRef.current.length) return draggedPathsRef.current;
    const raw = event.dataTransfer.getData("application/x-opskat-sftp-paths");
    if (!raw) return [];
    try {
      const parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
    } catch {
      return [];
    }
  };

  const getMovableDragPaths = (event: React.DragEvent, targetDirPath: string) => {
    return getDragPaths(event).filter((path) => canMovePathToDirectory(path, targetDirPath));
  };

  const getPointerDropTargetPath = (clientX: number, clientY: number, sourcePaths: string[]) => {
    const target = document.elementFromPoint(clientX, clientY);
    const row = target?.closest<HTMLElement>("[data-sftp-entry-row][data-sftp-entry-dir='true']");
    const targetPath = row?.dataset.sftpEntryPath;
    if (!targetPath) return null;
    return sourcePaths.some((path) => canMovePathToDirectory(path, targetPath)) ? targetPath : null;
  };

  const clearDragState = () => {
    draggedPathsRef.current = [];
    pointerDragRef.current = null;
    setDropTargetPath(null);
  };

  const beginPointerDrag = (path: string, event: React.PointerEvent<HTMLElement>) => {
    if (event.button !== 0 || event.shiftKey || event.ctrlKey || event.metaKey) return;
    const sourcePaths = selected.includes(path) ? selected : [path];
    pointerDragRef.current = {
      dragging: false,
      pointerId: event.pointerId,
      sourcePath: path,
      sourcePaths,
      startX: event.clientX,
      startY: event.clientY,
    };
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // Pointer capture is unavailable in jsdom and may fail if the pointer is already released.
    }
  };

  const updatePointerDrag = (event: React.PointerEvent<HTMLElement>) => {
    const drag = pointerDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const distance = Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY);
    if (!drag.dragging && distance < 6) return;
    if (!drag.dragging) {
      drag.dragging = true;
      suppressNextClickRef.current = true;
      setSelected(drag.sourcePaths);
    }
    event.preventDefault();
    setDropTargetPath(getPointerDropTargetPath(event.clientX, event.clientY, drag.sourcePaths));
  };

  const endPointerDrag = (event: React.PointerEvent<HTMLElement>) => {
    const drag = pointerDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const targetPath = drag.dragging ? getPointerDropTargetPath(event.clientX, event.clientY, drag.sourcePaths) : null;
    clearDragState();
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // Pointer capture is best-effort across browser/test environments.
    }
    if (!drag.dragging) return;
    event.preventDefault();
    event.stopPropagation();
    if (targetPath) onMoveEntriesToDirectory(drag.sourcePaths, targetPath);
    window.setTimeout(() => {
      suppressNextClickRef.current = false;
    }, 0);
  };

  return (
    <ScrollArea
      className="flex-1 min-h-0"
      onClick={(e) => {
        if (!isEntryTarget(e.target)) setSelected([]);
      }}
      onContextMenu={(e) => {
        if (isEntryTarget(e.target)) return;
        e.preventDefault();
        onOpenContextMenu(e.clientX, e.clientY, null);
      }}
    >
      <div className="text-xs select-none min-h-full">
        {loading && (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        )}
        {error && !loading && (
          <div className="flex flex-col items-center justify-center py-8 gap-1 px-2">
            <span className="text-destructive text-center text-xs">{t("sftp.loadError")}</span>
            <span className="text-muted-foreground text-center break-all text-[10px]">{error}</span>
            <Button variant="outline" size="xs" onClick={onRetry} className="mt-1">
              {t("sftp.retry")}
            </Button>
          </div>
        )}
        {!loading && !error && entries.length === 0 && (
          <div className="flex items-center justify-center py-8">
            <span className="text-muted-foreground">{t("sftp.empty")}</span>
          </div>
        )}
        {!loading && !error && (
          <>
            {currentPath !== "/" && (
              <div
                data-sftp-entry-row="true"
                data-sftp-entry-dir="true"
                data-sftp-entry-path={getParentPath(currentPath)}
                className={cn(
                  "flex items-center gap-1.5 px-2 py-1 cursor-pointer hover:bg-muted/50",
                  dropTargetPath === getParentPath(currentPath) && "bg-primary/10 ring-1 ring-primary/30"
                )}
                onDoubleClick={onGoUp}
              >
                <Folder className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                <span className="flex-1 truncate">..</span>
              </div>
            )}
            {sortedEntries.map((entry, index) => {
              const fullPath = getEntryPath(currentPath, entry);
              const isSelected = selected.includes(fullPath);
              const isCut = clipboardCutPaths.has(fullPath);
              const isRenaming = renamePath === fullPath;
              const isDropTarget = dropTargetPath === fullPath;
              return (
                <div
                  key={entry.name}
                  data-sftp-entry-row="true"
                  data-sftp-entry-dir={entry.isDir ? "true" : "false"}
                  data-sftp-entry-path={fullPath}
                  draggable={false}
                  className={cn(
                    "flex items-center gap-1.5 px-2 py-1 cursor-pointer transition-colors rounded-sm",
                    isSelected ? "bg-primary/15 text-primary" : "hover:bg-muted/50",
                    isCut && "opacity-45",
                    isDropTarget && "bg-primary/10 ring-1 ring-primary/30"
                  )}
                  style={{ contentVisibility: "auto", containIntrinsicSize: "auto 28px" }}
                  onDragStart={(e) => {
                    if (isRenaming) {
                      e.preventDefault();
                      return;
                    }
                    const paths = selected.includes(fullPath) ? selected : [fullPath];
                    draggedPathsRef.current = paths;
                    e.dataTransfer.effectAllowed = "move";
                    e.dataTransfer.setData("application/x-opskat-sftp-paths", JSON.stringify(paths));
                    e.dataTransfer.setData("text/plain", fullPath);
                    if (!selected.includes(fullPath)) setSelected([fullPath]);
                  }}
                  onDragEnd={clearDragState}
                  onDragOver={(e) => {
                    if (!entry.isDir || !getMovableDragPaths(e, fullPath).length) return;
                    e.preventDefault();
                    e.dataTransfer.dropEffect = "move";
                    setDropTargetPath(fullPath);
                  }}
                  onDragLeave={(e) => {
                    const nextTarget = e.relatedTarget;
                    if (nextTarget instanceof Node && e.currentTarget.contains(nextTarget)) return;
                    if (dropTargetPath === fullPath) setDropTargetPath(null);
                  }}
                  onDrop={(e) => {
                    if (!entry.isDir) return;
                    const sourcePaths = getMovableDragPaths(e, fullPath);
                    if (!sourcePaths.length) return;
                    e.preventDefault();
                    e.stopPropagation();
                    clearDragState();
                    onMoveEntriesToDirectory(sourcePaths, fullPath);
                  }}
                  onPointerDown={(e) => {
                    if (!isRenaming) beginPointerDrag(fullPath, e);
                  }}
                  onPointerMove={updatePointerDrag}
                  onPointerUp={endPointerDrag}
                  onPointerCancel={clearDragState}
                  onClick={(e) => {
                    if (suppressNextClickRef.current) {
                      suppressNextClickRef.current = false;
                      return;
                    }
                    if (isRenaming) return;
                    selectEntry(fullPath, index, e);
                    maybeStartSlowRename(fullPath, index, e.timeStamp);
                  }}
                  onDoubleClick={() => {
                    if (isRenaming) return;
                    if (entry.isDir) {
                      onNavigate(fullPath);
                      return;
                    }
                    if (canExternalEdit?.(entry)) {
                      onExternalOpen?.(fullPath);
                    }
                  }}
                  onContextMenu={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    if (!selected.includes(fullPath)) setSelected([fullPath]);
                    onOpenContextMenu(e.clientX, e.clientY, entry);
                  }}
                >
                  {entry.isDir ? (
                    <Folder className="h-3.5 w-3.5 text-primary/70 shrink-0" />
                  ) : (
                    <File className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                  )}
                  {isRenaming ? (
                    <RenameInput
                      key={fullPath}
                      initialName={entry.name}
                      onCommit={commitRename}
                      onCancel={onRenameCancel}
                    />
                  ) : (
                    <span className="flex-1 truncate">{entry.name}</span>
                  )}
                  {!entry.isDir && (
                    <span className="text-muted-foreground shrink-0 text-[10px]">{formatBytes(entry.size)}</span>
                  )}
                  <span className="text-muted-foreground shrink-0 text-[10px]">{formatDate(entry.modTime)}</span>
                </div>
              );
            })}
          </>
        )}
      </div>
    </ScrollArea>
  );
}
