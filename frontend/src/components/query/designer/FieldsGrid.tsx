import type { CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { KeyRound } from "lucide-react";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import type { DesignerColumn } from "./model";
import type { DraftColumn, DraftColumnPatch, DraftIssue } from "./draft";
import { EditableFieldRow } from "./EditableFieldRow";
import { AutoIncrementBadge, DefaultValue, PrimaryKeyMark } from "./FieldValues";

/** 列数固定、宽度按有没有注释列与操作列取。 */
function gridStyle(showComment: boolean, editable: boolean): CSSProperties {
  const columns = showComment
    ? ["36px", "28px", "minmax(130px,1fr)", "minmax(150px,1.2fr)", "52px", "minmax(140px,1fr)", "minmax(150px,1.4fr)"]
    : ["36px", "28px", "minmax(160px,1.2fr)", "minmax(170px,1.2fr)", "52px", "minmax(160px,1fr)"];
  if (editable) columns.push("34px");
  return { gridTemplateColumns: columns.join(" ") };
}

function FieldsHeader({
  style,
  showComment,
  editable,
}: {
  style: CSSProperties;
  showComment: boolean;
  editable: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="row"
      className="sticky top-0 z-10 grid h-7 items-center border-b border-border bg-muted/60 text-[11px] text-muted-foreground"
      style={style}
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
      {editable && <span role="columnheader" aria-label={t("query.designer.column.actions")} />}
    </div>
  );
}

interface FieldsGridProps {
  columns: DesignerColumn[];
  /** 是否有注释列(见 showsColumnComments)。 */
  showComment: boolean;
}

/** 字段子页:每列一行的紧凑表格(只读)。 */
export function FieldsGrid({ columns, showComment }: FieldsGridProps) {
  const { t } = useTranslation();
  const style = gridStyle(showComment, false);
  return (
    <div role="table" className="min-h-0 flex-1 overflow-auto">
      <FieldsHeader style={style} showComment={showComment} editable={false} />
      {columns.map((column, i) => (
        <div
          key={column.name}
          role="row"
          className="grid h-8 items-center border-b border-border/60 font-mono text-xs hover:bg-accent/30"
          style={style}
        >
          <span role="cell" className="pl-2.5 font-sans text-[11px] text-muted-foreground tabular-nums">
            {i + 1}
          </span>
          <span role="cell" className="flex justify-center">
            <PrimaryKeyMark primaryKey={column.primaryKey} />
          </span>
          <span role="cell" className="truncate px-2 select-text" title={column.name}>
            {column.name}
          </span>
          <span role="cell" className="flex min-w-0 items-center gap-1 px-2">
            <span className="truncate select-text" title={column.type}>
              {column.type}
            </span>
            {column.autoIncrement && <AutoIncrementBadge />}
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

interface EditableFieldsGridProps {
  columns: DraftColumn[];
  descriptor: DriverDescriptor | undefined;
  showComment: boolean;
  issues: DraftIssue[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
  onChange: (key: string, patch: DraftColumnPatch) => void;
}

/** 字段子页(设计表):同一张紧凑表格,格子直接编辑,带改动标记、锁定与问题标记,末尾是删除 / 恢复。 */
export function EditableFieldsGrid({
  columns,
  descriptor,
  showComment,
  issues,
  selectedKey,
  onSelect,
  onChange,
}: EditableFieldsGridProps) {
  const style = gridStyle(showComment, true);
  return (
    <div role="table" className="min-h-0 flex-1 overflow-auto">
      <FieldsHeader style={style} showComment={showComment} editable />
      {columns.map((column, i) => (
        <EditableFieldRow
          key={column.key}
          row={i + 1}
          column={column}
          descriptor={descriptor}
          style={style}
          showComment={showComment}
          issues={issues.filter((issue) => issue.target.kind === "column" && issue.target.key === column.key)}
          selected={selectedKey === column.key}
          onSelect={onSelect}
          onChange={onChange}
        />
      ))}
    </div>
  );
}
