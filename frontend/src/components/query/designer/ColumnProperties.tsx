import { useTranslation } from "react-i18next";
import { Lock } from "lucide-react";
import { Checkbox, Input, cn } from "@opskat/ui";
import { Segmented } from "@/components/asset/fields";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import type { DesignerDefault } from "./model";
import {
  autoIncrementEditable,
  changedFields,
  lockedFields,
  type ColumnField,
  type DraftColumn,
  type DraftColumnPatch,
} from "./draft";
import { fieldText, lockReason } from "./format";

type ExplicitKind = "none" | "null" | "value" | "expression";

interface ColumnPropertiesProps {
  column: DraftColumn;
  descriptor: DriverDescriptor | undefined;
  onChange: (key: string, patch: DraftColumnPatch) => void;
  onRevert: (key: string, field: ColumnField) => void;
  /** 新建表:自增只能在新表上设的驱动,列也可以勾自增。 */
  creating?: boolean;
}

/** 选中一列时表格下方的列属性:默认值(按驱动声明的写法或旧的单一文本)、自增、已修改的属性与原值。 */
export function ColumnProperties({ column, descriptor, onChange, onRevert, creating = false }: ColumnPropertiesProps) {
  const { t } = useTranslation();
  const key = column.key;
  const changed = changedFields(column);
  const defaultLocked = lockedFields(descriptor, column, creating).includes("default");
  return (
    <div data-testid="designer-column-props" className="shrink-0 border-t border-border bg-muted/15">
      <div className="flex h-7 items-center gap-2 border-b border-border/60 px-3 text-[11px]">
        <span className="font-medium">{t("query.designer.columnProps")}</span>
        <span className="truncate font-mono text-muted-foreground">
          {column.name || t("query.designer.unnamedColumn")}
        </span>
      </div>
      <div className="grid grid-cols-[1.3fr_0.8fr_1.2fr] gap-6 px-4 py-3 text-xs">
        <div className="min-w-0 space-y-2">
          <div className="text-[11px] text-muted-foreground">{t("query.designer.column.default")}</div>
          {defaultLocked ? (
            <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
              <Lock className="h-3 w-3 shrink-0" aria-hidden />
              {lockReason(t, descriptor, column, "default")}
            </div>
          ) : descriptor?.explicitDefaults ? (
            <ExplicitDefaultEditor
              value={column.default}
              onChange={(d) => onChange(key, { default: d })}
              cellId={`${key}:default`}
            />
          ) : (
            <LegacyDefaultEditor value={column.default} onChange={(d) => onChange(key, { default: d })} />
          )}
        </div>
        <div className="space-y-2">
          {descriptor?.autoIncrement && (
            <>
              <div className="text-[11px] text-muted-foreground">{t("query.designer.keys")}</div>
              <label className="flex items-center gap-2">
                <Checkbox
                  checked={column.autoIncrement}
                  // 已有列的自增不能改,只显示现状;自增只能在新表上设的驱动,已有表的新列也不能勾。
                  disabled={!autoIncrementEditable(descriptor, column, creating)}
                  onCheckedChange={(checked) => onChange(key, { autoIncrement: checked === true })}
                  aria-label={t("query.designer.autoIncrement")}
                />
                {t("query.designer.autoIncrement")}
              </label>
              {column.original ? (
                <p className="text-[10px] leading-4 text-muted-foreground">
                  {t("query.designer.autoIncrementExisting")}
                </p>
              ) : (
                descriptor.autoIncrementNewTablesOnly &&
                !creating && (
                  <p className="text-[10px] leading-4 text-muted-foreground">
                    {t("query.designer.autoIncrementNewTablesOnly")}
                  </p>
                )
              )}
            </>
          )}
        </div>
        {column.original && changed.length > 0 ? (
          <div className="min-w-0 space-y-1.5">
            <div className="text-[11px] text-muted-foreground">{t("query.designer.modifiedProps")}</div>
            {changed.map((field) => (
              <div key={field} className="flex items-center gap-2">
                <span className="w-14 shrink-0 truncate text-[11px] text-muted-foreground">
                  {t(`query.designer.column.${field}`)}
                </span>
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] line-through decoration-muted-foreground/60">
                  {fieldText(t, column.original!, field)}
                </span>
                <button
                  type="button"
                  className="shrink-0 text-[11px] text-primary hover:underline"
                  onClick={() => onRevert(key, field)}
                >
                  {t("query.designer.revert")}
                </button>
              </div>
            ))}
          </div>
        ) : (
          <div />
        )}
      </div>
    </div>
  );
}

/** 声明了默认值写法:无 / NULL / 值 / 表达式,值与表达式带文本输入。 */
function ExplicitDefaultEditor({
  value,
  onChange,
  cellId,
}: {
  value: DesignerDefault;
  onChange: (value: DesignerDefault) => void;
  /** 问题列表点到这一列的默认值时聚焦的格子。 */
  cellId: string;
}) {
  const { t } = useTranslation();
  // 驱动给了写法的列不会是 text;万一是,按「值」编辑原文。
  const kind: ExplicitKind = value.kind === "text" ? "value" : value.kind;
  const text = value.kind === "none" || value.kind === "null" ? "" : value.text;
  const setKind = (next: ExplicitKind) =>
    onChange(next === "none" || next === "null" ? { kind: next } : { kind: next, text });
  return (
    <>
      <Segmented
        aria-label={t("query.designer.column.default")}
        className="h-7 w-auto max-w-72 [&>button]:text-xs"
        value={kind}
        onChange={setKind}
        options={[
          { value: "none", label: t("query.designer.defaultKind.none") },
          { value: "null", label: "NULL" },
          { value: "value", label: t("query.designer.defaultKind.value") },
          { value: "expression", label: t("query.designer.defaultKind.expression") },
        ]}
      />
      <Input
        data-cell={cellId}
        aria-label={t("query.designer.defaultValue")}
        className={cn("h-7 font-mono text-xs")}
        value={text}
        disabled={kind === "none" || kind === "null"}
        onChange={(e) => onChange({ kind: kind === "expression" ? "expression" : "value", text: e.target.value })}
      />
      <p className="text-[10px] leading-4 text-muted-foreground">{t("query.designer.defaultHint.explicit")}</p>
    </>
  );
}

/** 没声明写法的驱动:单一文本输入,按旧规则由驱动判断写法。 */
function LegacyDefaultEditor({
  value,
  onChange,
}: {
  value: DesignerDefault;
  onChange: (value: DesignerDefault) => void;
}) {
  const { t } = useTranslation();
  const text = value.kind === "none" || value.kind === "null" ? "" : value.text;
  return (
    <>
      <Input
        aria-label={t("query.designer.defaultValue")}
        className="h-7 font-mono text-xs"
        value={text}
        placeholder={t("query.designer.defaultKind.none")}
        onChange={(e) => onChange(e.target.value === "" ? { kind: "none" } : { kind: "text", text: e.target.value })}
      />
      <p className="text-[10px] leading-4 text-muted-foreground">{t("query.designer.defaultHint.legacy")}</p>
    </>
  );
}
