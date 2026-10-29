import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, CircleAlert, Lock, RefreshCw, RotateCcw, Save } from "lucide-react";
import { Button, Input, Tooltip, TooltipContent, TooltipTrigger, cn } from "@opskat/ui";
import type { DesignerReadOnlyReason } from "./model";
import type { DraftCounts } from "./draft";

interface ReadOnlyToolbarProps {
  tableName: string;
  /** 表注释,读不出或没有时为空串。 */
  comment: string;
  /** 只读的原因;null 表示没有具体原因(只说只读);undefined 不标只读(设计表还在读取时)。 */
  readOnlyReason: DesignerReadOnlyReason | null | undefined;
  loading: boolean;
  onReload: () => void;
}

/** 只读设计器的工具栏:表名与表注释是文本,标出只读及原因,只有重新读取。 */
export function ReadOnlyToolbar({ tableName, comment, readOnlyReason, loading, onReload }: ReadOnlyToolbarProps) {
  const { t } = useTranslation();
  const reasonText =
    readOnlyReason === "readOnlyConnection"
      ? t("query.designer.readOnly.readOnlyConnection")
      : readOnlyReason === "driverCannotAlter"
        ? t("query.designer.readOnly.driverCannotAlter")
        : t("query.designer.readOnly.generic");
  return (
    <div className="flex h-10 shrink-0 items-center gap-3 border-b border-border px-3">
      <span data-testid="designer-table-name" className="truncate font-mono text-sm font-medium select-text">
        {tableName}
      </span>
      {comment && <span className="truncate text-xs text-muted-foreground select-text">{comment}</span>}
      {readOnlyReason !== undefined && (
        <span
          data-testid="designer-readonly"
          className="flex shrink-0 items-center gap-1 rounded-md bg-muted px-2 py-0.5 text-[11px] text-muted-foreground"
        >
          <Lock className="h-3 w-3" aria-hidden />
          {reasonText}
        </span>
      )}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="icon-xs"
            className="ml-auto"
            aria-label={t("query.designer.reload")}
            disabled={loading}
            onClick={onReload}
          >
            <RefreshCw className={cn(loading && "animate-spin")} />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("query.designer.reload")}</TooltipContent>
      </Tooltip>
    </div>
  );
}

interface DesignToolbarProps {
  /** 新建表:表名前的目标库(与 schema)选择;设计表时为空,表名前是「表名」。 */
  target?: ReactNode;
  tableName: string;
  onTableNameChange: (name: string) => void;
  /** 读出时的表名:改过时输入框标黄并悬停显示原值;新建表为 null(没有原值)。 */
  originalName: string | null;
  /** 表名上的问题:error 标红,warning 给出提醒文案。 */
  tableNameError: boolean;
  tableNameWarning?: string;
  /** 驱动不支持表注释时为 null:不显示表注释输入。 */
  comment: string | null;
  onCommentChange: (comment: string) => void;
  originalComment: string | null;
  /** 字段与索引各自的改动计数。 */
  counts: DraftCounts;
  indexCounts: DraftCounts;
  /** 新建表:代替改动计数显示的概要(几个字段、几个索引)。 */
  summary?: string;
  isDirty: boolean;
  errorCount: number;
  loading: boolean;
  /** 撤销全部改动与重新读取;新建表没有可重新读取的表,不显示这两个按钮。 */
  onRevertAll?: () => void;
  onReload?: () => void;
  /** 保存按钮能否点。 */
  canSave: boolean;
  onSave: () => void;
  /** 保存按钮的文字(新建表是「创建」),缺省为「保存」。 */
  saveLabel?: string;
  /** 保存快捷键的显示文本(快捷键设置里可改)。 */
  saveShortcut: string;
}

/** 设计表与新建表的工具栏:(新建表的目标库)表名、表注释、改动计数、问题数、撤销全部改动、重新读取与保存。 */
export function DesignToolbar({
  target,
  tableName,
  onTableNameChange,
  originalName,
  tableNameError,
  tableNameWarning,
  comment,
  onCommentChange,
  originalComment,
  counts,
  indexCounts,
  summary,
  isDirty,
  errorCount,
  loading,
  onRevertAll,
  onReload,
  canSave,
  onSave,
  saveLabel,
  saveShortcut,
}: DesignToolbarProps) {
  const { t } = useTranslation();
  const nameChanged = originalName !== null && tableName.trim() !== originalName;
  const commentChanged = comment !== null && originalComment !== null && comment.trim() !== originalComment.trim();
  const groups = [
    { label: t("query.designer.tab.fields"), counts },
    { label: t("query.designer.tab.indexes"), counts: indexCounts },
  ].filter((g) => g.counts.added + g.counts.modified + g.counts.deleted > 0);
  return (
    <div className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
      {target ?? <span className="shrink-0 text-[11px] text-muted-foreground">{t("query.designer.tableName")}</span>}
      <div className="relative flex items-center">
        <Input
          data-cell="table:name"
          data-testid="designer-table-name-input"
          aria-label={t("query.designer.tableName")}
          aria-invalid={tableNameError || undefined}
          title={nameChanged ? t("query.designer.originalValue", { value: originalName }) : tableNameWarning}
          className={cn(
            "h-7 w-44 font-mono text-xs",
            nameChanged && "bg-warning/10",
            tableNameWarning && "border-warning pr-6",
            tableNameError && "border-destructive bg-destructive/10"
          )}
          value={tableName}
          spellCheck={false}
          onChange={(e) => onTableNameChange(e.target.value)}
        />
        {tableNameWarning && (
          <AlertTriangle className="absolute right-2 h-3 w-3 text-warning" aria-label={tableNameWarning} />
        )}
      </div>
      {comment !== null && (
        <Input
          aria-label={t("query.designer.tableComment")}
          placeholder={t("query.designer.tableCommentPlaceholder")}
          title={commentChanged ? t("query.designer.originalValue", { value: originalComment || "—" }) : undefined}
          className={cn("h-7 w-64 text-xs", commentChanged && "bg-warning/10")}
          value={comment}
          onChange={(e) => onCommentChange(e.target.value)}
        />
      )}
      <div className="ml-auto flex shrink-0 items-center gap-2">
        <span data-testid="designer-changes" className="flex items-center gap-1 text-[11px] text-muted-foreground">
          {summary !== undefined
            ? summary
            : !isDirty
              ? t("query.designer.changes.none")
              : groups.length > 0
                ? groups.map((g, i) => (
                    <span key={g.label} className={cn("flex items-center gap-1", i > 0 && "ml-1.5")}>
                      {g.label}
                      {g.counts.added > 0 && <span className="text-success">+{g.counts.added}</span>}
                      {g.counts.modified > 0 && <span className="text-warning">~{g.counts.modified}</span>}
                      {g.counts.deleted > 0 && <span className="text-destructive">−{g.counts.deleted}</span>}
                    </span>
                  ))
                : t("query.designer.changes.tableOnly")}
        </span>
        {errorCount > 0 && (
          <span
            data-testid="designer-issue-count"
            className="flex items-center gap-1 rounded-md bg-destructive/10 px-2 py-0.5 text-[11px] text-destructive"
          >
            <CircleAlert className="h-3 w-3" aria-hidden />
            {t("query.designer.issues.count", { count: errorCount })}
          </span>
        )}
        {onRevertAll && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={t("query.designer.revertAll")}
                disabled={!isDirty || loading}
                onClick={onRevertAll}
              >
                <RotateCcw />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t("query.designer.revertAll")}</TooltipContent>
          </Tooltip>
        )}
        {onReload && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={t("query.designer.reload")}
                disabled={loading}
                onClick={onReload}
              >
                <RefreshCw className={cn(loading && "animate-spin")} />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t("query.designer.reload")}</TooltipContent>
          </Tooltip>
        )}
        <Button data-testid="designer-save" size="xs" className="h-7 px-2.5" disabled={!canSave} onClick={onSave}>
          <Save aria-hidden />
          {saveLabel ?? t("action.save")}
          <kbd className="font-sans text-[10px] opacity-70">{saveShortcut}</kbd>
        </Button>
      </div>
    </div>
  );
}
