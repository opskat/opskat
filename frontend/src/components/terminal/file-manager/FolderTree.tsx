import { memo, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, ChevronDown, ChevronRight, Folder, FolderOpen, Loader2 } from "lucide-react";
import { cn, ScrollArea } from "@opskat/ui";
import type { FolderTreeRow } from "@/lib/sftpDirTree";
import { sftp_svc } from "../../../../wailsjs/go/models";
import { RenameInput } from "./RenameInput";

const ROW_PADDING_PX = 6;
const INDENT_STEP_PX = 12;
/** 占位行(加载中/失败)跳过箭头位(14px)与间距(4px),与同层目录名对齐。 */
const PLACEHOLDER_OFFSET_PX = 18;

function indentOf(depth: number) {
  return ROW_PADDING_PX + depth * INDENT_STEP_PX;
}

interface FolderTreeProps {
  rows: FolderTreeRow[];
  currentPath: string;
  onNavigate: (path: string) => void;
  onToggle: (path: string) => void;
  onRetry: (path: string) => void;
  /** 拖动经过的目录(由面板判定合法后才会落到这里)。 */
  dropTargetPath: string | null;
  onDirDragOver: (event: React.DragEvent, path: string) => void;
  onDirDragLeave: (event: React.DragEvent, path: string) => void;
  onDirDrop: (event: React.DragEvent, path: string) => void;
  /** 右键目录行:菜单与列表里的目录行相同,作用对象只是这一行。 */
  onDirContextMenu: (x: number, y: number, path: string, entry: sftp_svc.FileEntry) => void;
  /** 正在树行内重命名的目录。 */
  renamingPath: string | null;
  onRenameCommit: (path: string, nextName: string) => void;
  onRenameCancel: () => void;
  className?: string;
}

/**
 * 文件面板里的目录树:根恒为 /、只列目录。它不持有当前目录,只高亮面板的当前目录;
 * 单击行等价于在列表里双击进入该目录,箭头只展开/收起。
 */
export function FolderTree({
  rows,
  currentPath,
  onNavigate,
  onToggle,
  onRetry,
  dropTargetPath,
  onDirDragOver,
  onDirDragLeave,
  onDirDrop,
  onDirContextMenu,
  renamingPath,
  onRenameCommit,
  onRenameCancel,
  className,
}: FolderTreeProps) {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const revealedRef = useRef<string | null>(null);
  const dirRows = useMemo(() => rows.filter((row) => row.state === "dir"), [rows]);
  // 漫游 tabindex:整棵树只有一项可 Tab 进入。默认是当前目录;键盘/点击移走后跟着焦点项,
  // 焦点项因祖先收起而消失时退回当前目录(再不行就第一行)。
  const [focusedPath, setFocusedPath] = useState<string | null>(null);
  const tabStopPath = useMemo(() => {
    if (focusedPath && dirRows.some((row) => row.path === focusedPath)) return focusedPath;
    if (dirRows.some((row) => row.path === currentPath)) return currentPath;
    return dirRows[0]?.path ?? null;
  }, [currentPath, dirRows, focusedPath]);

  const focusRow = useCallback((path: string) => {
    setFocusedPath(path);
    containerRef.current?.querySelector<HTMLElement>(`[data-sftp-tree-path="${CSS.escape(path)}"]`)?.focus();
  }, []);

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      // 行内重命名的输入框自己处理回车/方向键,不能再被当成树的导航。
      if ((event.target as HTMLElement).tagName === "INPUT") return;
      const path = (event.target as HTMLElement).closest<HTMLElement>("[data-sftp-tree-path]")?.dataset.sftpTreePath;
      const index = dirRows.findIndex((row) => row.path === path);
      if (index < 0) return;
      const row = dirRows[index];
      switch (event.key) {
        case "ArrowDown":
          if (index + 1 < dirRows.length) focusRow(dirRows[index + 1].path);
          break;
        case "ArrowUp":
          if (index > 0) focusRow(dirRows[index - 1].path);
          break;
        case "ArrowRight": {
          if (row.leaf) break;
          if (!row.expanded) {
            onToggle(row.path);
            break;
          }
          const child = dirRows[index + 1];
          if (child && child.depth === row.depth + 1) focusRow(child.path);
          break;
        }
        case "ArrowLeft": {
          if (row.expanded && !row.leaf) {
            onToggle(row.path);
            break;
          }
          for (let i = index - 1; i >= 0; i--) {
            if (dirRows[i].depth === row.depth - 1) {
              focusRow(dirRows[i].path);
              break;
            }
          }
          break;
        }
        case "Enter":
          onNavigate(row.path);
          break;
        default:
          return;
      }
      event.preventDefault();
    },
    [dirRows, focusRow, onNavigate, onToggle]
  );

  // 当前目录行一出现就滚到可见(已可见时 nearest 不滚动);同一个当前目录只滚一次,
  // 之后用户自己滚走不会被拉回来。祖先逐层加载期间该行可能稍后才出现,所以按行是否存在判断。
  useEffect(() => {
    if (revealedRef.current === currentPath) return;
    const row = containerRef.current?.querySelector<HTMLElement>('[data-sftp-tree-current="true"]');
    if (!row) return;
    revealedRef.current = currentPath;
    row.scrollIntoView?.({ block: "nearest" });
  }, [currentPath, rows]);

  return (
    <ScrollArea className={cn("min-h-0", className)}>
      <div
        ref={containerRef}
        data-testid="sftp-folder-tree"
        role="tree"
        aria-label={t("sftp.folderTree.label")}
        className="py-0.5 text-xs select-none"
        onKeyDown={handleKeyDown}
      >
        {rows.map((row) =>
          row.state === "dir" ? (
            <FolderTreeDirRow
              key={`dir:${row.path}`}
              row={row}
              isCurrent={row.path === currentPath}
              isTabStop={row.path === tabStopPath}
              isDropTarget={row.path === dropTargetPath}
              onDragOver={onDirDragOver}
              onDragLeave={onDirDragLeave}
              onDrop={onDirDrop}
              onContextMenu={onDirContextMenu}
              isRenaming={row.path === renamingPath}
              onRenameCommit={onRenameCommit}
              onRenameCancel={onRenameCancel}
              onFocusRow={setFocusedPath}
              onNavigate={onNavigate}
              onToggle={onToggle}
              expandLabel={t("sftp.tree.expand")}
              collapseLabel={t("sftp.tree.collapse")}
            />
          ) : (
            <FolderTreeStatusRow key={`${row.state}:${row.path}`} row={row} onRetry={onRetry} />
          )
        )}
      </div>
    </ScrollArea>
  );
}

function TreeGuides({ depth }: { depth: number }) {
  if (depth <= 0) return null;
  return (
    <>
      {Array.from({ length: depth }, (_, level) => (
        <span
          key={level}
          aria-hidden="true"
          className="pointer-events-none absolute top-0 bottom-0 w-px bg-border"
          style={{ left: indentOf(level) + 6 }}
        />
      ))}
    </>
  );
}

interface FolderTreeDirRowProps {
  row: FolderTreeRow;
  isCurrent: boolean;
  isTabStop: boolean;
  isDropTarget: boolean;
  onDragOver: (event: React.DragEvent, path: string) => void;
  onDragLeave: (event: React.DragEvent, path: string) => void;
  onDrop: (event: React.DragEvent, path: string) => void;
  onContextMenu: (x: number, y: number, path: string, entry: sftp_svc.FileEntry) => void;
  isRenaming: boolean;
  onRenameCommit: (path: string, nextName: string) => void;
  onRenameCancel: () => void;
  onFocusRow: (path: string) => void;
  onNavigate: (path: string) => void;
  onToggle: (path: string) => void;
  expandLabel: string;
  collapseLabel: string;
}

const FolderTreeDirRow = memo(function FolderTreeDirRow({
  row,
  isCurrent,
  isTabStop,
  isDropTarget,
  onDragOver,
  onDragLeave,
  onDrop,
  onContextMenu,
  isRenaming,
  onRenameCommit,
  onRenameCancel,
  onFocusRow,
  onNavigate,
  onToggle,
  expandLabel,
  collapseLabel,
}: FolderTreeDirRowProps) {
  const Icon = row.expanded ? FolderOpen : Folder;
  return (
    <div
      data-testid={`sftp-tree-row-${row.path}`}
      data-sftp-tree-path={row.path}
      data-sftp-tree-current={isCurrent ? "true" : undefined}
      data-sftp-tree-drop={isDropTarget ? "true" : undefined}
      role="treeitem"
      aria-label={row.name}
      aria-level={row.depth + 1}
      aria-expanded={row.leaf ? undefined : row.expanded}
      aria-selected={isCurrent}
      tabIndex={isTabStop ? 0 : -1}
      title={row.path}
      className={cn(
        "relative flex cursor-pointer items-center gap-1 rounded-sm py-1 pr-2 outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-inset",
        isCurrent ? "bg-primary/15 font-medium text-primary" : "hover:bg-muted/50",
        isDropTarget && "bg-primary/10 ring-1 ring-primary/30"
      )}
      style={{ paddingLeft: indentOf(row.depth) }}
      onFocus={() => onFocusRow(row.path)}
      onDragOver={(e) => onDragOver(e, row.path)}
      onDragLeave={(e) => onDragLeave(e, row.path)}
      onDrop={(e) => onDrop(e, row.path)}
      onClick={() => {
        if (!isRenaming) onNavigate(row.path);
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        e.stopPropagation();
        // 根 / 没有可操作的条目(不能重命名、删除或移动它)。
        if (row.entry) onContextMenu(e.clientX, e.clientY, row.path, row.entry);
      }}
    >
      <TreeGuides depth={row.depth} />
      {row.leaf ? (
        <span className="w-3.5 shrink-0" />
      ) : (
        <button
          type="button"
          data-testid={`sftp-tree-toggle-${row.path}`}
          aria-label={row.expanded ? collapseLabel : expandLabel}
          aria-hidden="true"
          tabIndex={-1}
          className="flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={(e) => {
            e.stopPropagation();
            onToggle(row.path);
          }}
        >
          {row.loading ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : row.expanded ? (
            <ChevronDown className="h-3 w-3" />
          ) : (
            <ChevronRight className="h-3 w-3" />
          )}
        </button>
      )}
      <Icon className={cn("h-3.5 w-3.5 shrink-0", isCurrent ? "text-primary" : "text-primary/70")} />
      {isRenaming ? (
        <RenameInput
          initialName={row.name}
          onCommit={(name) => onRenameCommit(row.path, name)}
          onCancel={onRenameCancel}
        />
      ) : (
        <span className="min-w-0 flex-1 truncate">{row.name}</span>
      )}
    </div>
  );
});

function FolderTreeStatusRow({ row, onRetry }: { row: FolderTreeRow; onRetry: (path: string) => void }) {
  const { t } = useTranslation();
  const style = { paddingLeft: indentOf(row.depth) + PLACEHOLDER_OFFSET_PX };
  if (row.state === "loading") {
    return (
      <div className="relative py-1 pr-2 text-[11px] text-muted-foreground" style={style}>
        <TreeGuides depth={row.depth} />
        {t("sftp.tree.loading")}
      </div>
    );
  }
  return (
    <div className="relative flex items-center gap-1.5 py-1 pr-2 text-[11px] text-destructive" style={style}>
      <TreeGuides depth={row.depth} />
      <AlertTriangle className="h-3 w-3 shrink-0" />
      <span className="min-w-0 flex-1 truncate" title={row.message ?? undefined}>
        {row.message}
      </span>
      <button
        type="button"
        data-testid={`sftp-tree-retry-${row.path}`}
        className="shrink-0 text-primary hover:underline"
        onClick={() => onRetry(row.path)}
      >
        {t("sftp.retry")}
      </button>
    </div>
  );
}
