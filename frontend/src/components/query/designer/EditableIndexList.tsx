import type { CSSProperties, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Lock, Trash2, Undo2 } from "lucide-react";
import { Button, Tooltip, TooltipContent, TooltipTrigger, cn } from "@opskat/ui";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import type { ColumnStatus, DraftIssue, TableDraft } from "./draft";
import {
  changedIndexFields,
  indexDefinition,
  indexDroppable,
  indexDroppedWith,
  indexLock,
  indexStatus,
  type DraftIndex,
  type IndexField,
} from "./indexDraft";
import { IndexColumnChips, IndexListHeader, IndexTypeBadge } from "./IndexList";
import { indexGridStyle, indexLockText, optionText } from "./format";

const STATUS_BAR: Record<ColumnStatus, string> = {
  new: "bg-success",
  modified: "bg-warning",
  deleted: "bg-destructive",
  unchanged: "",
};

interface EditableIndexListProps {
  draft: TableDraft;
  descriptor: DriverDescriptor | undefined;
  showComment: boolean;
  issues: DraftIssue[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  onDelete: (key: string) => void;
  onRestore: (key: string) => void;
}

/**
 * 索引子页(设计表):同字段表的改动标记(新增绿条、修改黄条与原值、删除红条加删除线),随列删除的注明
 * 原因;锁定的已有索引带锁与原因;点一行在右侧编辑区打开。
 */
export function EditableIndexList({
  draft,
  descriptor,
  showComment,
  issues,
  selectedKey,
  onSelect,
  onDelete,
  onRestore,
}: EditableIndexListProps) {
  const options = descriptor?.indexEditing?.options ?? [];
  const style = indexGridStyle(options.length, showComment, true);
  return (
    <div role="table" data-testid="designer-index-list" className="min-h-0 flex-1 overflow-auto">
      <IndexListHeader descriptor={descriptor} style={style} showComment={showComment} actions />
      {draft.indexes.map((index, i) => (
        <EditableIndexRow
          key={index.key}
          row={i + 1}
          draft={draft}
          index={index}
          descriptor={descriptor}
          style={style}
          showComment={showComment}
          issues={issues.filter((issue) => issue.target.kind === "index" && issue.target.key === index.key)}
          selected={selectedKey === index.key}
          onSelect={onSelect}
          onDelete={onDelete}
          onRestore={onRestore}
        />
      ))}
    </div>
  );
}

interface EditableIndexRowProps {
  row: number;
  draft: TableDraft;
  index: DraftIndex;
  descriptor: DriverDescriptor | undefined;
  style: CSSProperties;
  showComment: boolean;
  issues: DraftIssue[];
  selected: boolean;
  onSelect: (key: string) => void;
  onDelete: (key: string) => void;
  onRestore: (key: string) => void;
}

function EditableIndexRow({
  row,
  draft,
  index,
  descriptor,
  style,
  showComment,
  issues,
  selected,
  onSelect,
  onDelete,
  onRestore,
}: EditableIndexRowProps) {
  const { t } = useTranslation();

  const options = descriptor?.indexEditing?.options ?? [];
  const status = indexStatus(draft, index);
  const deleted = status === "deleted";
  const droppedWith = indexDroppedWith(draft, index);
  const changed = deleted ? [] : changedIndexFields(index);
  const definition = indexDefinition(draft, index);
  const original = index.original;
  const lock = indexLock(descriptor, index);
  const lockText = lock ? indexLockText(t, lock, original?.readOnlyReason ?? "") : "";
  const issueOn = (field: IndexField, severity: DraftIssue["severity"]) =>
    !deleted && issues.some((i) => i.severity === severity && i.target.kind === "index" && i.target.field === field);
  const errorOn = (field: IndexField) => issueOn(field, "error");
  const warningOn = (field: IndexField) => issueOn(field, "warning");
  const cell = (field: IndexField, className: string, children: ReactNode, originalText?: string) => (
    <span
      role="cell"
      data-changed={changed.includes(field)}
      data-error={errorOn(field)}
      title={changed.includes(field) ? t("query.designer.originalValue", { value: originalText }) : undefined}
      className={cn(
        "flex h-full min-w-0 items-center",
        changed.includes(field) && "bg-warning/10",
        warningOn(field) && "ring-1 ring-warning ring-inset",
        errorOn(field) && "bg-destructive/10 ring-1 ring-destructive ring-inset",
        className
      )}
    >
      {children}
    </span>
  );
  const originalColumns = original ? original.columns.map((c) => c.name).join(", ") : "";

  return (
    <div
      role="row"
      data-status={status}
      onClick={() => onSelect(index.key)}
      className={cn(
        "relative grid min-h-9 cursor-default items-center border-b border-border/60 text-xs",
        selected ? "bg-accent/60" : "hover:bg-accent/30",
        deleted && "bg-destructive/5 text-muted-foreground"
      )}
      style={style}
    >
      {status !== "unchanged" && (
        <span className={cn("absolute inset-y-0 left-0 w-[3px]", STATUS_BAR[status])} aria-hidden />
      )}
      <span role="cell" className="pl-2.5 text-[11px] text-muted-foreground tabular-nums">
        {row}
      </span>
      {cell(
        "name",
        "gap-1 px-2 font-mono",
        <>
          <span className={cn("truncate select-text", deleted && "line-through")} title={index.name}>
            {index.name || <span className="text-muted-foreground/60 italic">{t("query.designer.unnamedIndex")}</span>}
          </span>
          {lock && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Lock
                  className="h-3 w-3 shrink-0 text-muted-foreground/70"
                  aria-label={lock === "readOnly" ? t("query.designer.indexReadOnly") : lockText}
                />
              </TooltipTrigger>
              <TooltipContent>{lockText}</TooltipContent>
            </Tooltip>
          )}
        </>,
        original?.name
      )}
      {cell("type", "", <IndexTypeBadge descriptor={descriptor} type={index.type} />, original?.type)}
      {cell(
        "columns",
        "flex-wrap gap-1 px-2 py-1",
        <>
          <IndexColumnChips index={definition} struck={deleted} />
          {original?.readOnlyReason && !deleted && (
            <span className="ml-1 truncate text-[10px] text-muted-foreground" title={original.readOnlyReason}>
              {original.readOnlyReason}
            </span>
          )}
          {droppedWith.length > 0 && (
            <span className="ml-1 truncate text-[10px] text-destructive">
              {t("query.designer.indexDroppedWith", {
                columns: droppedWith.join(t("query.designer.listSeparator")),
              })}
            </span>
          )}
          {status === "modified" && original && (
            <span className="ml-1 truncate text-[10px] text-warning">
              {t("query.designer.indexWas", { name: original.name, columns: originalColumns })}
            </span>
          )}
        </>,
        originalColumns
      )}
      {options.map((o) =>
        cell(
          `option:${o.key}`,
          "font-mono text-[11px] text-muted-foreground",
          <span className="truncate">{optionText(t, o.type, index.options[o.key])}</span>,
          original ? optionText(t, o.type, original.options[o.key]) : undefined
        )
      )}
      {showComment &&
        cell(
          "comment",
          "px-2 text-muted-foreground",
          <span className="truncate select-text" title={index.comment}>
            {index.comment || "—"}
          </span>,
          original?.comment || "—"
        )}
      <span role="cell" className="flex justify-center" onClick={(e) => e.stopPropagation()}>
        {index.deleted ? (
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={t("query.designer.restoreIndex")}
            title={t("query.designer.restoreIndex")}
            onClick={() => onRestore(index.key)}
          >
            <Undo2 />
          </Button>
        ) : (
          !deleted &&
          indexDroppable(descriptor, index) && (
            <Button
              variant="ghost"
              size="icon-xs"
              className="text-muted-foreground hover:text-destructive"
              aria-label={t("query.designer.deleteIndex")}
              title={t("query.designer.deleteIndex")}
              onClick={() => onDelete(index.key)}
            >
              <Trash2 />
            </Button>
          )
        )}
      </span>
    </div>
  );
}
