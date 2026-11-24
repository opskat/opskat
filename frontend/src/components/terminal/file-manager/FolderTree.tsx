import { memo, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, ChevronDown, ChevronRight, Folder, FolderOpen, Loader2 } from "lucide-react";
import { cn, ScrollArea } from "@opskat/ui";
import type { FolderTreeRow } from "@/lib/sftpDirTree";

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
  className?: string;
}

/**
 * 文件面板里的目录树:根恒为 /、只列目录。它不持有当前目录,只高亮面板的当前目录;
 * 单击行等价于在列表里双击进入该目录,箭头只展开/收起。
 */
export function FolderTree({ rows, currentPath, onNavigate, onToggle, onRetry, className }: FolderTreeProps) {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const revealedRef = useRef<string | null>(null);

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
        aria-label={t("sftp.folderTree.label")}
        className="py-0.5 text-xs select-none"
      >
        {rows.map((row) =>
          row.state === "dir" ? (
            <FolderTreeDirRow
              key={`dir:${row.path}`}
              row={row}
              isCurrent={row.path === currentPath}
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
  onNavigate: (path: string) => void;
  onToggle: (path: string) => void;
  expandLabel: string;
  collapseLabel: string;
}

const FolderTreeDirRow = memo(function FolderTreeDirRow({
  row,
  isCurrent,
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
      title={row.path}
      className={cn(
        "relative flex cursor-pointer items-center gap-1 rounded-sm py-1 pr-2",
        isCurrent ? "bg-primary/15 font-medium text-primary" : "hover:bg-muted/50"
      )}
      style={{ paddingLeft: indentOf(row.depth) }}
      onClick={() => onNavigate(row.path)}
    >
      <TreeGuides depth={row.depth} />
      {row.leaf ? (
        <span className="w-3.5 shrink-0" />
      ) : (
        <button
          type="button"
          data-testid={`sftp-tree-toggle-${row.path}`}
          aria-label={row.expanded ? collapseLabel : expandLabel}
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
      <span className="min-w-0 flex-1 truncate">{row.name}</span>
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
