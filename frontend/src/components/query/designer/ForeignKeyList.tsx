import { useTranslation } from "react-i18next";
import { Lock } from "lucide-react";
import type { DesignerForeignKey } from "./model";

const GRID = "grid grid-cols-[minmax(160px,1fr)_minmax(120px,1fr)_minmax(180px,1fr)_100px_100px] items-center px-3";

/** 外键子页:只读列出名称、列、引用表与列、ON UPDATE、ON DELETE。 */
export function ForeignKeyList({ foreignKeys }: { foreignKeys: DesignerForeignKey[] }) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-1.5 border-b border-border bg-muted/20 px-3 py-1.5 text-[11px] text-muted-foreground">
        <Lock className="h-3 w-3" aria-hidden />
        {t("query.designer.foreignKeysReadOnly")}
      </div>
      <div role="table" className="min-h-0 flex-1 overflow-auto">
        <div
          role="row"
          className={`${GRID} sticky top-0 z-10 h-7 border-b border-border bg-muted/60 text-[11px] text-muted-foreground`}
        >
          <span role="columnheader">{t("query.designer.column.name")}</span>
          <span role="columnheader">{t("query.designer.column.fkColumns")}</span>
          <span role="columnheader">{t("query.designer.column.references")}</span>
          <span role="columnheader">ON UPDATE</span>
          <span role="columnheader">ON DELETE</span>
        </div>
        {foreignKeys.map((fk, i) => (
          <div
            key={`${i}:${fk.name}`}
            role="row"
            className={`${GRID} h-8 border-b border-border/60 font-mono text-xs select-text`}
          >
            <span role="cell" className="truncate" title={fk.name}>
              {fk.name || "—"}
            </span>
            <span role="cell" className="truncate">
              {fk.columns.join(", ")}
            </span>
            <span role="cell" className="truncate">
              {`${fk.referencedTable} (${fk.referencedColumns.join(", ")})`}
            </span>
            <span role="cell" className="text-muted-foreground">
              {fk.onUpdate || "—"}
            </span>
            <span role="cell" className="text-muted-foreground">
              {fk.onDelete || "—"}
            </span>
          </div>
        ))}
        {foreignKeys.length === 0 && (
          <div className="px-3 py-6 text-center text-xs text-muted-foreground">{t("query.designer.noForeignKeys")}</div>
        )}
      </div>
    </div>
  );
}
