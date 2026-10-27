import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowDown, Info, Lock } from "lucide-react";
import { cn } from "@opskat/ui";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import { declaredLabel, indexTypeLabel, showsIndexComments, type DesignerIndex } from "./model";

const TYPE_STYLE: Record<string, string> = {
  primary: "bg-warning/15 text-warning",
  unique: "bg-info/15 text-info",
  fulltext: "bg-success/15 text-success",
};

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
  const template = ["36px", "minmax(170px,1fr)", "64px", "minmax(220px,1.8fr)"];
  for (let i = 0; i < options.length; i++) template.push("80px");
  if (showComment) template.push("minmax(90px,0.8fr)");
  // 列数由驱动声明的选项决定,只能运行时算出。
  const style = { gridTemplateColumns: template.join(" ") };

  const optionText = (type: string, value: unknown) => {
    if (type === "switch") return value ? t("query.designer.yes") : "—";
    return value === undefined || value === "" ? t("query.designer.optionDefault") : String(value);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {notEditableNotice && (
        <div className="flex shrink-0 items-start gap-2 border-b border-border bg-info/10 px-3 py-1.5 text-[11px] text-info">
          <Info className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
          <span>{t("query.designer.indexesNotEditable")}</span>
          {updateDriverAction}
        </div>
      )}
      <div role="table" className="min-h-0 flex-1 overflow-auto">
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
        </div>
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
              <span
                className={cn(
                  "rounded px-1.5 py-0.5 text-[10px]",
                  TYPE_STYLE[index.type] ?? "bg-muted text-muted-foreground"
                )}
              >
                {indexTypeLabel(descriptor, index.type, t)}
              </span>
            </span>
            <span role="cell" className="flex min-w-0 flex-wrap items-center gap-1 px-2 py-1">
              {index.columns.map((column, c) => (
                <span
                  key={`${c}:${column.name}`}
                  data-testid="index-column"
                  data-expression={column.expression ? "true" : undefined}
                  className={cn(
                    "inline-flex shrink-0 items-center gap-0.5 rounded border border-border bg-background px-1.5 py-px font-mono text-[11px] select-text",
                    column.expression && "border-dashed italic"
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
              {index.readOnlyReason && (
                <span className="ml-1 truncate text-[10px] text-muted-foreground" title={index.readOnlyReason}>
                  {index.readOnlyReason}
                </span>
              )}
            </span>
            {options.map((o) => (
              <span role="cell" key={o.key} className="truncate font-mono text-[11px] text-muted-foreground">
                {optionText(o.type, index.options[o.key])}
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
