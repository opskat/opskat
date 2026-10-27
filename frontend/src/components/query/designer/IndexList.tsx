import type { CSSProperties, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowDown, Info, Lock } from "lucide-react";
import { cn } from "@opskat/ui";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import { declaredLabel, indexTypeLabel, showsIndexComments, type DesignerIndex } from "./model";
import { indexGridStyle, optionText } from "./format";

const TYPE_STYLE: Record<string, string> = {
  primary: "bg-warning/15 text-warning",
  unique: "bg-info/15 text-info",
  fulltext: "bg-success/15 text-success",
};

/** 索引表的表头。 */
export function IndexListHeader({
  descriptor,
  style,
  showComment,
  actions,
}: {
  descriptor: DriverDescriptor | undefined;
  style: CSSProperties;
  showComment: boolean;
  actions: boolean;
}) {
  const { t } = useTranslation();
  const options = descriptor?.indexEditing?.options ?? [];
  return (
    <div
      role="row"
      className="sticky top-0 z-10 grid h-7 items-center border-b border-border bg-muted/60 text-[11px] text-muted-foreground"
      style={style}
    >
      <span role="columnheader" className="pl-2">
        #
      </span>
      <span role="columnheader" className="px-2">
        {t("query.designer.column.name")}
      </span>
      <span role="columnheader">{t("query.designer.column.type")}</span>
      <span role="columnheader" className="px-2">
        {t("query.designer.column.indexColumns")}
      </span>
      {options.map((o) => (
        <span role="columnheader" key={o.key} className="truncate">
          {declaredLabel(t, o.label)}
        </span>
      ))}
      {showComment && (
        <span role="columnheader" className="px-2">
          {t("query.designer.column.comment")}
        </span>
      )}
      {actions && <span role="columnheader" aria-label={t("query.designer.column.actions")} />}
    </div>
  );
}

/** 索引类型的标签:驱动声明的名称,内置类型按颜色区分。 */
export function IndexTypeBadge({ descriptor, type }: { descriptor: DriverDescriptor | undefined; type: string }) {
  const { t } = useTranslation();
  return (
    <span className={cn("rounded px-1.5 py-0.5 text-[10px]", TYPE_STYLE[type] ?? "bg-muted text-muted-foreground")}>
      {indexTypeLabel(descriptor, type, t)}
    </span>
  );
}

/** 索引的列:按次序,降序带箭头,表达式是虚线框,前缀长度写在括号里,INCLUDE 列跟在后面。 */
export function IndexColumnChips({ index, struck }: { index: DesignerIndex; struck?: boolean }) {
  const { t } = useTranslation();
  return (
    <>
      {index.columns.map((column, c) => (
        <span
          key={`${c}:${column.name}`}
          data-testid="index-column"
          data-expression={column.expression ? "true" : undefined}
          className={cn(
            "inline-flex shrink-0 items-center gap-0.5 rounded border border-border bg-background px-1.5 py-px font-mono text-[11px] select-text",
            column.expression && "border-dashed italic",
            struck && "line-through"
          )}
        >
          {column.prefixLength > 0 ? `${column.name}(${column.prefixLength})` : column.name}
          {column.descending && (
            <ArrowDown className="h-3 w-3 text-muted-foreground" aria-label={t("query.designer.descending")} />
          )}
        </span>
      ))}
      {index.include.length > 0 && (
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
          INCLUDE ({index.include.join(", ")})
        </span>
      )}
    </>
  );
}

/** 驱动不能在设计器里改索引时索引页顶部的说明,后面可以跟「去更新」入口。 */
export function IndexesNotEditableNotice({ action }: { action?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className="flex shrink-0 items-start gap-2 border-b border-border bg-info/10 px-3 py-1.5 text-[11px] text-info">
      <Info className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
      <span>{t("query.designer.indexesNotEditable")}</span>
      {action}
    </div>
  );
}

interface IndexListProps {
  indexes: DesignerIndex[];
  descriptor: DriverDescriptor | undefined;
  /** 驱动不能在设计器里改索引(改表模型不带索引):顶部说明只能查看。 */
  notEditableNotice?: boolean;
  /** 说明后面的「去更新」入口(扩展驱动可更新时由调用方给出)。 */
  updateDriverAction?: ReactNode;
}

/** 索引子页的列表(只读):类型、按次序的列、驱动声明的选项、注释,只读索引带锁与原因。 */
export function IndexList({ indexes, descriptor, notEditableNotice, updateDriverAction }: IndexListProps) {
  const { t } = useTranslation();
  const options = descriptor?.indexEditing?.options ?? [];
  const showComment = showsIndexComments(descriptor, indexes);
  const style = indexGridStyle(options.length, showComment, false);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {notEditableNotice && <IndexesNotEditableNotice action={updateDriverAction} />}
      <div role="table" className="min-h-0 flex-1 overflow-auto">
        <IndexListHeader descriptor={descriptor} style={style} showComment={showComment} actions={false} />
        {indexes.map((index, i) => (
          <div
            key={index.name}
            role="row"
            className="grid min-h-9 items-center border-b border-border/60 text-xs hover:bg-accent/30"
            style={style}
          >
            <span role="cell" className="pl-2 text-[11px] text-muted-foreground tabular-nums">
              {i + 1}
            </span>
            <span role="cell" className="flex min-w-0 items-center gap-1 px-2 font-mono">
              <span className="truncate select-text" title={index.name}>
                {index.name}
              </span>
              {index.readOnlyReason && (
                <Lock
                  className="h-3 w-3 shrink-0 text-muted-foreground/70"
                  aria-label={t("query.designer.indexReadOnly")}
                />
              )}
            </span>
            <span role="cell">
              <IndexTypeBadge descriptor={descriptor} type={index.type} />
            </span>
            <span role="cell" className="flex min-w-0 flex-wrap items-center gap-1 px-2 py-1">
              <IndexColumnChips index={index} />
              {index.readOnlyReason && (
                <span className="ml-1 truncate text-[10px] text-muted-foreground" title={index.readOnlyReason}>
                  {index.readOnlyReason}
                </span>
              )}
            </span>
            {options.map((o) => (
              <span role="cell" key={o.key} className="truncate font-mono text-[11px] text-muted-foreground">
                {optionText(t, o.type, index.options[o.key])}
              </span>
            ))}
            {showComment && (
              <span role="cell" className="truncate px-2 text-muted-foreground select-text" title={index.comment}>
                {index.comment || "—"}
              </span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
