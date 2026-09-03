import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
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
  // 父组件 FileManagerPanel 把 onNavigate / onOpenContextMenu / onRenameCancel 等
  // 写成内联箭头函数,而 selected 也住在父组件 —— 选中一变父组件就重渲,这些 prop
  // 全部换标识。若把它们直接传给行,行的 memo 一次都不会命中(实测反而更慢,因为白
  // 付了一遍 props 比较)。这里镜像进 ref,对外只暴露标识恒定的包装。
  const cbRef = useRef({
    canExternalEdit,
    onExternalOpen,
    onNavigate,
    onOpenContextMenu,
    onMoveEntriesToDirectory,
    onRenameCancel,
    onRenameCommit,
    renamePath,
  });
  useEffect(() => {
    cbRef.current = {
      canExternalEdit,
      onExternalOpen,
      onNavigate,
      onOpenContextMenu,
      onMoveEntriesToDirectory,
      onRenameCancel,
      onRenameCommit,
      renamePath,
    };
  }, [
    canExternalEdit,
    onExternalOpen,
    onNavigate,
    onOpenContextMenu,
    onMoveEntriesToDirectory,
    onRenameCancel,
    onRenameCommit,
    renamePath,
  ]);

  const stableCanExternalEdit = useCallback(
    (entry: sftp_svc.FileEntry) => cbRef.current.canExternalEdit?.(entry) ?? false,
    []
  );
  const stableExternalOpen = useCallback((path: string) => cbRef.current.onExternalOpen?.(path), []);
  const stableNavigate = useCallback((path: string) => cbRef.current.onNavigate(path), []);
  const stableOpenContextMenu = useCallback(
    (x: number, y: number, entry: sftp_svc.FileEntry | null) => cbRef.current.onOpenContextMenu(x, y, entry),
    []
  );
  const stableMoveEntries = useCallback(
    (sourcePaths: string[], targetDirPath: string) =>
      cbRef.current.onMoveEntriesToDirectory(sourcePaths, targetDirPath),
    []
  );
  const stableRenameCancel = useCallback(() => cbRef.current.onRenameCancel(), []);

  // 命中判断用 Set:以前每行都跑一次 selected.includes(),n 行 × O(选中数) 在
  // 全选(shift 选完整个目录)时退化成 O(n²)。
  const selectedSet = useMemo(() => new Set(selected), [selected]);
  // 事件回调需要"当前选中集合",但不能把它放进依赖 —— 否则每次改选中回调就换标识,
  // 把所有行的 memo 全部打掉(和 QueryResultTable 里 handleCellContextMenu 同一个坑)。
  const selectedRef = useRef(selected);
  useEffect(() => {
    selectedRef.current = selected;
  }, [selected]);

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

  const entryPathsRef = useRef(entryPaths);
  useEffect(() => {
    entryPathsRef.current = entryPaths;
  }, [entryPaths]);

  const selectEntry = useCallback(
    (path: string, index: number, event: React.MouseEvent) => {
      if (event.shiftKey && lastClickedRef.current !== null) {
        const start = Math.min(lastClickedRef.current, index);
        const end = Math.max(lastClickedRef.current, index);
        setSelected(entryPathsRef.current.slice(start, end + 1));
        return;
      }
      if (event.ctrlKey || event.metaKey) {
        setSelected((prev) => (prev.includes(path) ? prev.filter((item) => item !== path) : [...prev, path]));
        lastClickedRef.current = index;
        return;
      }
      setSelected([path]);
      lastClickedRef.current = index;
    },
    [setSelected]
  );

  const maybeStartSlowRename = useCallback(
    (path: string, index: number, eventTime: number) => {
      const now = eventTime;
      const prev = slowClickRef.current;
      const sel = selectedRef.current;
      if (prev.timer) window.clearTimeout(prev.timer);
      if (
        prev.path === path &&
        now - prev.time > 450 &&
        now - prev.time < 1400 &&
        sel.length === 1 &&
        sel[0] === path
      ) {
        prev.path = "";
        prev.time = 0;
        stableOpenContextMenu(-1, -1, null); // closes any pending menu in parent no-op path
        window.dispatchEvent(new CustomEvent("sftp:rename-request", { detail: { path } }));
        return;
      }
      slowClickRef.current = { path, time: now, timer: null };
      slowClickRef.current.timer = window.setTimeout(() => {
        if (slowClickRef.current.path === path) slowClickRef.current.path = "";
      }, 1500);
      lastClickedRef.current = index;
    },
    [stableOpenContextMenu]
  );

  const commitRename = useCallback((nextName: string) => {
    const { renamePath: path, onRenameCancel: cancel, onRenameCommit: commit } = cbRef.current;
    if (!path) return;
    const trimmed = nextName.trim();
    if (!trimmed) {
      cancel();
      return;
    }
    commit(path, trimmed);
  }, []);

  const isEntryTarget = (target: EventTarget | null) => {
    return target instanceof Element && !!target.closest("[data-sftp-entry-row]");
  };

  const getDragPaths = useCallback((event: React.DragEvent) => {
    if (draggedPathsRef.current.length) return draggedPathsRef.current;
    const raw = event.dataTransfer.getData("application/x-opskat-sftp-paths");
    if (!raw) return [];
    try {
      const parsed = JSON.parse(raw);
      return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
    } catch {
      return [];
    }
  }, []);

  const getMovableDragPaths = useCallback(
    (event: React.DragEvent, targetDirPath: string) =>
      getDragPaths(event).filter((path) => canMovePathToDirectory(path, targetDirPath)),
    [getDragPaths]
  );

  const getPointerDropTargetPath = useCallback((clientX: number, clientY: number, sourcePaths: string[]) => {
    const target = document.elementFromPoint(clientX, clientY);
    const row = target?.closest<HTMLElement>("[data-sftp-entry-row][data-sftp-entry-dir='true']");
    const targetPath = row?.dataset.sftpEntryPath;
    if (!targetPath) return null;
    return sourcePaths.some((path) => canMovePathToDirectory(path, targetPath)) ? targetPath : null;
  }, []);

  const clearDragState = useCallback(() => {
    draggedPathsRef.current = [];
    pointerDragRef.current = null;
    setDropTargetPath(null);
  }, []);

  const beginPointerDrag = useCallback((path: string, event: React.PointerEvent<HTMLElement>) => {
    if (event.button !== 0 || event.shiftKey || event.ctrlKey || event.metaKey) return;
    const sel = selectedRef.current;
    const sourcePaths = sel.includes(path) ? sel : [path];
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
  }, []);

  const updatePointerDrag = useCallback(
    (event: React.PointerEvent<HTMLElement>) => {
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
    },
    [getPointerDropTargetPath, setSelected]
  );

  const endPointerDrag = useCallback(
    (event: React.PointerEvent<HTMLElement>) => {
      const drag = pointerDragRef.current;
      if (!drag || drag.pointerId !== event.pointerId) return;
      const targetPath = drag.dragging
        ? getPointerDropTargetPath(event.clientX, event.clientY, drag.sourcePaths)
        : null;
      clearDragState();
      try {
        event.currentTarget.releasePointerCapture(event.pointerId);
      } catch {
        // Pointer capture is best-effort across browser/test environments.
      }
      if (!drag.dragging) return;
      event.preventDefault();
      event.stopPropagation();
      if (targetPath) stableMoveEntries(drag.sourcePaths, targetPath);
      window.setTimeout(() => {
        suppressNextClickRef.current = false;
      }, 0);
    },
    [clearDragState, getPointerDropTargetPath, stableMoveEntries]
  );

  // 所有行共用同一个 handlers 对象:每行 26 个 prop 时,5000 行全部改选中(shift 全选)
  // 要多付一遍 26×5000 的浅比较和 props 分配,反而比不 memo 更慢。收成一个恒定标识的
  // 对象后,行的 prop 面只剩 entry + 4 个布尔 + h。
  const rowHandlers = useMemo(
    () => ({
      canExternalEdit: stableCanExternalEdit,
      onExternalOpen: stableExternalOpen,
      onNavigate: stableNavigate,
      onOpenContextMenu: stableOpenContextMenu,
      onMoveEntriesToDirectory: stableMoveEntries,
      onRenameCancel: stableRenameCancel,
      commitRename,
      selectEntry,
      maybeStartSlowRename,
      selectedRef,
      setSelected,
      draggedPathsRef,
      suppressNextClickRef,
      getMovableDragPaths,
      setDropTargetPath,
      clearDragState,
      beginPointerDrag,
      updatePointerDrag,
      endPointerDrag,
    }),
    [
      beginPointerDrag,
      clearDragState,
      commitRename,
      endPointerDrag,
      getMovableDragPaths,
      maybeStartSlowRename,
      selectEntry,
      setSelected,
      stableCanExternalEdit,
      stableExternalOpen,
      stableMoveEntries,
      stableNavigate,
      stableOpenContextMenu,
      stableRenameCancel,
      updatePointerDrag,
    ]
  );

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
              return (
                <FileRow
                  key={entry.name}
                  entry={entry}
                  fullPath={fullPath}
                  index={index}
                  isSelected={selectedSet.has(fullPath)}
                  isCut={clipboardCutPaths.has(fullPath)}
                  isRenaming={renamePath === fullPath}
                  isDropTarget={dropTargetPath === fullPath}
                  h={rowHandlers}
                />
              );
            })}
          </>
        )}
      </div>
    </ScrollArea>
  );
}

interface FileRowHandlers {
  canExternalEdit: (entry: sftp_svc.FileEntry) => boolean;
  onExternalOpen: (path: string) => void;
  onNavigate: (path: string) => void;
  onOpenContextMenu: (x: number, y: number, entry: sftp_svc.FileEntry | null) => void;
  onMoveEntriesToDirectory: (sourcePaths: string[], targetDirPath: string) => void;
  onRenameCancel: () => void;
  commitRename: (nextName: string) => void;
  selectEntry: (path: string, index: number, event: React.MouseEvent) => void;
  maybeStartSlowRename: (path: string, index: number, eventTime: number) => void;
  selectedRef: React.RefObject<string[]>;
  setSelected: (next: string[] | ((prev: string[]) => string[])) => void;
  draggedPathsRef: React.RefObject<string[]>;
  suppressNextClickRef: React.RefObject<boolean>;
  getMovableDragPaths: (event: React.DragEvent, targetDirPath: string) => string[];
  setDropTargetPath: (path: string | null) => void;
  clearDragState: () => void;
  beginPointerDrag: (path: string, event: React.PointerEvent<HTMLElement>) => void;
  updatePointerDrag: (event: React.PointerEvent<HTMLElement>) => void;
  endPointerDrag: (event: React.PointerEvent<HTMLElement>) => void;
}

interface FileRowProps {
  entry: sftp_svc.FileEntry;
  fullPath: string;
  index: number;
  isSelected: boolean;
  isCut: boolean;
  isRenaming: boolean;
  isDropTarget: boolean;
  h: FileRowHandlers;
}

// 单行 memo 化:选中态是列表级 state,不 memo 的话点一行会把整个目录的行全部重渲。
// 5000 个文件的目录实测单击一次 631ms、shift 全选 1.6s(见 PR 说明)。所有需要
// "当前选中集合"的回调都通过 ref 读,保证 props 在选中变化时保持同一标识。
const FileRow = memo(function FileRow({
  entry,
  fullPath,
  index,
  isSelected,
  isCut,
  isRenaming,
  isDropTarget,
  h,
}: FileRowProps) {
  return (
    <div
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
        const { selectedRef, draggedPathsRef } = h;
        const sel = selectedRef.current;
        const paths = sel.includes(fullPath) ? sel : [fullPath];
        draggedPathsRef.current = paths;
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("application/x-opskat-sftp-paths", JSON.stringify(paths));
        e.dataTransfer.setData("text/plain", fullPath);
        if (!sel.includes(fullPath)) h.setSelected([fullPath]);
      }}
      onDragEnd={h.clearDragState}
      onDragOver={(e) => {
        if (!entry.isDir || !h.getMovableDragPaths(e, fullPath).length) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        h.setDropTargetPath(fullPath);
      }}
      onDragLeave={(e) => {
        const nextTarget = e.relatedTarget;
        if (nextTarget instanceof Node && e.currentTarget.contains(nextTarget)) return;
        if (isDropTarget) h.setDropTargetPath(null);
      }}
      onDrop={(e) => {
        if (!entry.isDir) return;
        const sourcePaths = h.getMovableDragPaths(e, fullPath);
        if (!sourcePaths.length) return;
        e.preventDefault();
        e.stopPropagation();
        h.clearDragState();
        h.onMoveEntriesToDirectory(sourcePaths, fullPath);
      }}
      onPointerDown={(e) => {
        if (!isRenaming) h.beginPointerDrag(fullPath, e);
      }}
      onPointerMove={h.updatePointerDrag}
      onPointerUp={h.endPointerDrag}
      onPointerCancel={h.clearDragState}
      onClick={(e) => {
        const { suppressNextClickRef } = h;
        if (suppressNextClickRef.current) {
          suppressNextClickRef.current = false;
          return;
        }
        if (isRenaming) return;
        h.selectEntry(fullPath, index, e);
        h.maybeStartSlowRename(fullPath, index, e.timeStamp);
      }}
      onDoubleClick={() => {
        if (isRenaming) return;
        if (entry.isDir) {
          h.onNavigate(fullPath);
          return;
        }
        if (h.canExternalEdit?.(entry)) {
          h.onExternalOpen?.(fullPath);
        }
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
        if (!h.selectedRef.current.includes(fullPath)) h.setSelected([fullPath]);
        h.onOpenContextMenu(e.clientX, e.clientY, entry);
      }}
    >
      {entry.isDir ? (
        <Folder className="h-3.5 w-3.5 text-primary/70 shrink-0" />
      ) : (
        <File className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
      )}
      {isRenaming ? (
        <RenameInput key={fullPath} initialName={entry.name} onCommit={h.commitRename} onCancel={h.onRenameCancel} />
      ) : (
        <span className="flex-1 truncate">{entry.name}</span>
      )}
      {!entry.isDir && <span className="text-muted-foreground shrink-0 text-[10px]">{formatBytes(entry.size)}</span>}
      <span className="text-muted-foreground shrink-0 text-[10px]">{formatDate(entry.modTime)}</span>
    </div>
  );
});
