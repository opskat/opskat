import type { CSSProperties } from "react";
import type { TFunction } from "i18next";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import type { DesignerDefault } from "./model";
import { fieldAttribute, type ColumnField, type DraftColumn } from "./draft";
import type { IndexLock } from "./indexDraft";

/** 按字面量显示为数字(不加引号)的值。 */
export const NUMERIC_LITERAL = /^-?\d+(\.\d+)?$/;

/** 默认值的纯文本(悬停原值、列属性里的原值用)。 */
export function defaultValueText(t: TFunction, value: DesignerDefault): string {
  switch (value.kind) {
    case "none":
      return t("query.designer.defaultKind.none");
    case "null":
      return "NULL";
    case "value":
      return NUMERIC_LITERAL.test(value.text) ? value.text : `'${value.text}'`;
    case "expression":
    case "text":
      return value.text;
  }
}

/** 一列的某个属性显示成文本:悬停原值、列属性里的原值都用它。 */
export function fieldText(t: TFunction, column: Pick<DraftColumn, ColumnField>, field: ColumnField): string {
  switch (field) {
    case "nullable":
      return column.nullable ? t("query.designer.yes") : t("query.designer.no");
    case "default":
      return defaultValueText(t, column.default);
    case "comment":
      return column.comment || "—";
    default:
      return column[field];
  }
}

/** 锁定原因:已有列由驱动声明的可改属性生成;新列只可能锁注释(驱动不能写列注释)。 */
export function lockReason(
  t: TFunction,
  descriptor: DriverDescriptor | undefined,
  column: DraftColumn,
  field: Exclude<ColumnField, "name">
): string {
  if (!column.original) return t("query.designer.lockReasonComment", { driver: descriptor?.name });
  return t("query.designer.lockReason", {
    driver: descriptor?.name,
    attribute: t(`query.designer.attribute.${fieldAttribute(field)}`),
  });
}

/** 索引表的列宽:序号、名称、类型、列,驱动声明的各选项,注释(有时),操作(设计表时)。 */
export function indexGridStyle(optionCount: number, showComment: boolean, actions: boolean): CSSProperties {
  const template = ["36px", "minmax(170px,1fr)", "64px", "minmax(220px,1.8fr)"];
  for (let i = 0; i < optionCount; i++) template.push("80px");
  if (showComment) template.push("minmax(90px,0.8fr)");
  if (actions) template.push("34px");
  // 列数由驱动声明的选项决定,只能运行时算出。
  return { gridTemplateColumns: template.join(" ") };
}

/** 索引选项的取值显示:开关打开为「是」,关闭为 —;枚举没给为「默认」。 */
export function optionText(t: TFunction, type: string, value: unknown): string {
  if (type === "switch") return value ? t("query.designer.yes") : "—";
  return value === undefined || value === "" ? t("query.designer.optionDefault") : String(value);
}

/** 锁定的已有索引为什么不能改:只读索引是驱动给的原因,其余由锁定的种类得出。 */
export function indexLockText(t: TFunction, lock: IndexLock, readOnlyReason: string): string {
  switch (lock) {
    case "readOnly":
      return readOnlyReason;
    case "undroppable":
      return t("query.designer.indexLock.undroppable");
    case "primaryKey":
      return t("query.designer.indexLock.primaryKey");
  }
}

/** 语句拼成一段脚本:每条以分号结尾,条与条之间空一行(复制与在 SQL 标签打开共用)。 */
export function statementsScript(statements: string[]): string {
  return statements.map((s) => (s.trimEnd().endsWith(";") ? s.trimEnd() : `${s.trimEnd()};`)).join("\n\n");
}
