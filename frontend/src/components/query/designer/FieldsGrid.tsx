import { useTranslation } from "react-i18next";
import { KeyRound } from "lucide-react";
import { cn } from "@opskat/ui";
import type { DesignerColumn, DesignerDefault } from "./model";

const NUMERIC_LITERAL = /^-?\d+(\.\d+)?$/;

/** 默认值按写法显示:无为破折号,NULL 斜体,值按字面量(数字 / 加引号的文本),表达式带 ƒx,原文照登。 */
export function DefaultValue({ value }: { value: DesignerDefault }) {
  switch (value.kind) {
    case "none":
      return <span className="text-muted-foreground/50">—</span>;
    case "null":
      return <span className="text-syntax-null italic">NULL</span>;
    case "value":
      return NUMERIC_LITERAL.test(value.text) ? (
        <span className="text-syntax-number">{value.text}</span>
      ) : (
        <span className="text-syntax-string">&apos;{value.text}&apos;</span>
      );
    case "expression":
      return (
        <span className="inline-flex min-w-0 items-center gap-1">
          <span className="rounded bg-info/15 px-1 font-sans text-[10px] text-info">ƒx</span>
          <span className="truncate">{value.text}</span>
        </span>
      );
    case "text":
      return <span>{value.text}</span>;
  }
}

interface FieldsGridProps {
  columns: DesignerColumn[];
  /** 是否有注释列(见 showsColumnComments)。 */
  showComment: boolean;
}

/** 字段子页:每列一行的紧凑表格(只读)。 */
export function FieldsGrid({ columns, showComment }: FieldsGridProps) {
  const { t } = useTranslation();
  const grid = showComment
    ? "grid-cols-[36px_28px_minmax(130px,1fr)_minmax(150px,1.2fr)_52px_minmax(140px,1fr)_minmax(150px,1.4fr)]"
    : "grid-cols-[36px_28px_minmax(160px,1.2fr)_minmax(170px,1.2fr)_52px_minmax(160px,1fr)]";
  return (
    <div role="table" className="min-h-0 flex-1 overflow-auto">
      <div
        role="row"
        className={cn(
          "sticky top-0 z-10 grid h-7 items-center border-b border-border bg-muted/60 text-[11px] text-muted-foreground",
          grid
        )}
      >
        <span role="columnheader" className="pl-2">
          #
        </span>
        <span role="columnheader" className="flex justify-center" aria-label={t("query.designer.column.primaryKey")}>
          <KeyRound className="h-3 w-3" aria-hidden />
        </span>
        <span role="columnheader" className="px-2">
          {t("query.designer.column.name")}
        </span>
        <span role="columnheader" className="px-2">
          {t("query.designer.column.type")}
        </span>
        <span role="columnheader" className="text-center">
          {t("query.designer.column.nullable")}
        </span>
        <span role="columnheader" className="px-2">
          {t("query.designer.column.default")}
        </span>
        {showComment && (
          <span role="columnheader" className="px-2">
            {t("query.designer.column.comment")}
          </span>
        )}
      </div>
      {columns.map((column, i) => (
        <div
          key={column.name}
          role="row"
          className={cn("grid h-8 items-center border-b border-border/60 font-mono text-xs hover:bg-accent/30", grid)}
        >
          <span role="cell" className="pl-2.5 font-sans text-[11px] text-muted-foreground tabular-nums">
            {i + 1}
          </span>
          <span role="cell" className="flex justify-center">
            {column.primaryKey ? (
              <KeyRound className="h-3.5 w-3.5 text-warning" aria-label={t("query.designer.column.primaryKey")} />
            ) : (
              <KeyRound className="h-3.5 w-3.5 text-muted-foreground/20" aria-hidden />
            )}
          </span>
          <span role="cell" className="truncate px-2 select-text" title={column.name}>
            {column.name}
          </span>
          <span role="cell" className="flex min-w-0 items-center gap-1 px-2">
            <span className="truncate select-text" title={column.type}>
              {column.type}
            </span>
            {column.autoIncrement && (
              <span className="shrink-0 rounded bg-muted px-1 font-sans text-[10px] text-muted-foreground">
                {t("query.designer.autoIncrement")}
              </span>
            )}
          </span>
          <span role="cell" className="text-center font-sans text-[11px] text-muted-foreground">
            {column.nullable ? t("query.designer.yes") : t("query.designer.no")}
          </span>
          <span role="cell" className="flex min-w-0 items-center truncate px-2 select-text">
            <DefaultValue value={column.default} />
          </span>
          {showComment && (
            <span role="cell" className="truncate px-2 font-sans select-text" title={column.comment}>
              {column.comment || <span className="text-muted-foreground/40">—</span>}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}
