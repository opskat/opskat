import { memo, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Lock, Trash2, Undo2 } from "lucide-react";
import { Button, Checkbox, Tooltip, TooltipContent, TooltipTrigger, cn } from "@opskat/ui";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import {
  changedFields,
  columnStatus,
  lockedFields,
  type ColumnField,
  type ColumnStatus,
  type DraftColumn,
  type DraftColumnPatch,
  type DraftIssue,
} from "./draft";
import { issueMessage } from "./issueText";
import { CellInput } from "./CellInput";
import { TypeInput } from "./TypeInput";
import { AutoIncrementBadge, DefaultValue, PrimaryKeyMark } from "./FieldValues";
import { fieldText, lockReason } from "./format";

const STATUS_BAR: Record<ColumnStatus, string> = {
  new: "bg-success",
  modified: "bg-warning",
  deleted: "bg-destructive",
  unchanged: "",
};

interface FieldCellProps {
  changed: boolean;
  /** 改过时悬停显示的原值。 */
  original?: string;
  error?: boolean;
  warning?: string;
  lock?: string;
  className?: string;
  children: ReactNode;
}

/** 一个格子:改过的底色变黄并悬停显示原值,出错标红,提醒带黄色三角,锁定带锁与原因。 */
function FieldCell({ changed, original, error, warning, lock, className, children }: FieldCellProps) {
  const { t } = useTranslation();
  return (
    <div
      role="cell"
      data-changed={changed}
      data-error={!!error}
      title={changed ? t("query.designer.originalValue", { value: original }) : undefined}
      className={cn(
        "relative flex h-full min-w-0 items-center",
        changed && "bg-warning/10",
        warning && "ring-1 ring-warning ring-inset",
        error && "bg-destructive/10 ring-1 ring-destructive ring-inset",
        className
      )}
    >
      {children}
      {warning && <AlertTriangle className="mr-1 h-3 w-3 shrink-0 text-warning" aria-label={warning} />}
      {lock && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Lock className="mr-1.5 ml-1 h-3 w-3 shrink-0 text-muted-foreground/60" aria-label={lock} />
          </TooltipTrigger>
          <TooltipContent>{lock}</TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}

interface EditableFieldRowProps {
  /** 表格里的序号(从 1 起)。 */
  row: number;
  column: DraftColumn;
  descriptor: DriverDescriptor | undefined;
  style: CSSProperties;
  showComment: boolean;
  /** 指向这一列的问题。 */
  issues: DraftIssue[];
  selected: boolean;
  onSelect: (key: string) => void;
  onChange: (key: string, patch: DraftColumnPatch) => void;
}

/** 字段表里可编辑的一行:左侧色条标出新增 / 修改 / 删除,删除的已有列划掉并可恢复。 */
export const EditableFieldRow = memo(function EditableFieldRow({
  row,
  column,
  descriptor,
  style,
  showComment,
  issues,
  selected,
  onSelect,
  onChange,
}: EditableFieldRowProps) {
  const { t } = useTranslation();
  const status = columnStatus(column);
  const deleted = status === "deleted";
  const changed = changedFields(column);
  const locked = lockedFields(descriptor, column);
  const key = column.key;
  const cellId = (field: "name" | "type") => `${key}:${field}`;

  const issueOf = (field: "name" | "type", severity: DraftIssue["severity"]) =>
    issues.find((i) => i.target.kind === "column" && i.target.field === field && i.severity === severity);
  const cellProps = (field: ColumnField) => {
    const isChanged = !deleted && changed.includes(field);
    const warning = field === "name" || field === "type" ? issueOf(field, "warning") : undefined;
    return {
      changed: isChanged,
      original: isChanged && column.original ? fieldText(t, column.original, field) : undefined,
      error: !deleted && (field === "name" || field === "type") && !!issueOf(field, "error"),
      warning: !deleted && warning ? issueMessage(t, warning) : undefined,
      lock:
        !deleted && field !== "name" && locked.includes(field) ? lockReason(t, descriptor, column, field) : undefined,
    };
  };
  // 删除的行与锁定的格子显示文本。
  const plain = (text: ReactNode, title?: string) => (
    <span className={cn("min-w-0 flex-1 truncate px-2", deleted && "line-through")} title={title}>
      {text}
    </span>
  );
  const editable = (field: ColumnField) => !deleted && !locked.includes(field);

  return (
    <div
      role="row"
      data-status={status}
      onClick={() => onSelect(key)}
      onFocusCapture={() => onSelect(key)}
      className={cn(
        "relative grid h-8 items-center border-b border-border/60 font-mono text-xs",
        selected ? "bg-accent/60" : "hover:bg-accent/30",
        deleted && "bg-destructive/5 text-muted-foreground"
      )}
      style={style}
    >
      {status !== "unchanged" && (
        <span className={cn("absolute inset-y-0 left-0 w-[3px]", STATUS_BAR[status])} aria-hidden />
      )}
      <span role="cell" className="pl-2.5 font-sans text-[11px] text-muted-foreground tabular-nums">
        {row}
      </span>
      <span role="cell" className="flex justify-center">
        <PrimaryKeyMark primaryKey={column.primaryKey} />
      </span>
      <FieldCell {...cellProps("name")}>
        {deleted ? (
          plain(column.name, column.name)
        ) : (
          <CellInput
            value={column.name}
            onChange={(name) => onChange(key, { name })}
            aria-label={t("query.designer.column.name")}
            placeholder={t("query.designer.column.name")}
            cellId={cellId("name")}
            invalid={cellProps("name").error}
          />
        )}
      </FieldCell>
      <FieldCell {...cellProps("type")}>
        {editable("type") ? (
          <TypeInput
            value={column.type}
            onChange={(type) => onChange(key, { type })}
            descriptor={descriptor}
            cellId={cellId("type")}
            invalid={cellProps("type").error}
          />
        ) : (
          plain(column.type, column.type)
        )}
        {column.autoIncrement && (
          <span className="mr-1">
            <AutoIncrementBadge />
          </span>
        )}
      </FieldCell>
      <FieldCell {...cellProps("nullable")} className="justify-center">
        {editable("nullable") ? (
          <Checkbox
            checked={column.nullable}
            onCheckedChange={(checked) => onChange(key, { nullable: checked === true })}
            aria-label={t("query.designer.column.nullable")}
          />
        ) : (
          <span className={cn("font-sans text-[11px] text-muted-foreground", deleted && "line-through")}>
            {fieldText(t, column, "nullable")}
          </span>
        )}
      </FieldCell>
      <FieldCell {...cellProps("default")}>{plain(<DefaultValue value={column.default} />)}</FieldCell>
      {showComment && (
        <FieldCell {...cellProps("comment")}>
          {editable("comment") ? (
            <CellInput
              value={column.comment}
              onChange={(comment) => onChange(key, { comment })}
              aria-label={t("query.designer.column.comment")}
              cellId={`${key}:comment`}
              className="font-sans"
            />
          ) : (
            plain(<span className="font-sans">{column.comment || "—"}</span>, column.comment)
          )}
        </FieldCell>
      )}
      <span role="cell" className="flex justify-center">
        {deleted ? (
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={t("query.designer.restoreColumn")}
            title={t("query.designer.restoreColumn")}
            onClick={() => onChange(key, { deleted: false })}
          >
            <Undo2 />
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-muted-foreground hover:text-destructive"
            aria-label={t("query.designer.deleteColumn")}
            title={t("query.designer.deleteColumn")}
            onClick={() => onChange(key, { deleted: true })}
          >
            <Trash2 />
          </Button>
        )}
      </span>
    </div>
  );
});
