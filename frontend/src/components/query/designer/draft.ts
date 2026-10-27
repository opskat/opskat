import { dbdriver } from "../../../../wailsjs/go/models";
import { driverCanAlter, type DriverDescriptor } from "@/lib/dbDrivers";
import type { DesignerColumn, DesignerDefault, DesignerIndex, TableStructure } from "./model";

/** 字段表里一列可编辑的属性。 */
export type ColumnField = "name" | "type" | "nullable" | "default" | "comment";

/** 已有列的属性对应的驱动可改属性(Descriptor.alterTableAttributes 的取值)。 */
const FIELD_ATTRIBUTE: Record<Exclude<ColumnField, "name">, string> = {
  type: "column_type",
  nullable: "column_nullable",
  default: "column_default",
  comment: "column_comment",
};

/** 能力说明条按这个次序列出可改与不可改的属性。 */
const ALTER_ATTRIBUTES = ["column_type", "column_nullable", "column_default", "column_comment", "table_comment"];

/** 草稿里的一列:original 是读出时的样子,新增的列为 null;已有列删除只是标记,保存前可恢复。 */
export interface DraftColumn {
  key: string;
  original: DesignerColumn | null;
  deleted: boolean;
  name: string;
  type: string;
  nullable: boolean;
  default: DesignerDefault;
  comment: string;
  primaryKey: boolean;
  autoIncrement: boolean;
}

/** 设计器的草稿:表名、表注释与各列(已有的按读出的次序在前,新增的在后)。 */
export interface TableDraft {
  /** 读出时的表名与表注释。 */
  original: { name: string; comment: string };
  name: string;
  comment: string;
  columns: DraftColumn[];
  /** 下一个新增列的编号,用来生成 key。 */
  nextId: number;
}

export type DraftColumnPatch = Partial<
  Pick<DraftColumn, "deleted" | "name" | "type" | "nullable" | "default" | "comment" | "autoIncrement">
>;

export function initDraft(structure: TableStructure): TableDraft {
  return {
    original: { name: structure.name, comment: structure.comment },
    name: structure.name,
    comment: structure.comment,
    columns: structure.columns.map((c) => ({ key: `e:${c.name}`, original: c, deleted: false, ...c })),
    nextId: 1,
  };
}

/** 加一列:类型预填驱动声明的新列类型,没声明时留空(校验要求填写)。 */
export function addColumn(
  draft: TableDraft,
  descriptor: DriverDescriptor | undefined
): { draft: TableDraft; key: string } {
  const key = `n:${draft.nextId}`;
  const column: DraftColumn = {
    key,
    original: null,
    deleted: false,
    name: "",
    type: descriptor?.columnTypes?.newColumn ?? "",
    nullable: true,
    default: { kind: "none" },
    comment: "",
    primaryKey: false,
    autoIncrement: false,
  };
  return { draft: { ...draft, columns: [...draft.columns, column], nextId: draft.nextId + 1 }, key };
}

/** 改一列;删除新增的列是直接移除,删除已有列只做标记。 */
export function updateColumn(draft: TableDraft, key: string, patch: DraftColumnPatch): TableDraft {
  const target = draft.columns.find((c) => c.key === key);
  if (!target) return draft;
  if (patch.deleted && !target.original) {
    return { ...draft, columns: draft.columns.filter((c) => c.key !== key) };
  }
  return { ...draft, columns: draft.columns.map((c) => (c.key === key ? { ...c, ...patch } : c)) };
}

/** 把一列的某个属性还原成读出时的值(只对已有列有意义)。 */
export function revertField(column: DraftColumn, field: ColumnField): DraftColumn {
  if (!column.original) return column;
  return { ...column, [field]: column.original[field] };
}

function defaultText(value: DesignerDefault): string {
  return value.kind === "none" || value.kind === "null" ? "" : value.text;
}

function sameDefault(a: DesignerDefault, b: DesignerDefault): boolean {
  return a.kind === b.kind && defaultText(a).trim() === defaultText(b).trim();
}

/**
 * 已有列上改过的属性,按表格的次序。比较方式同驱动:名字去掉首尾空白,类型不分大小写,
 * 默认值与注释去掉首尾空白。
 */
export function changedFields(column: DraftColumn): ColumnField[] {
  const o = column.original;
  if (!o) return [];
  const out: ColumnField[] = [];
  if (column.name.trim() !== o.name.trim()) out.push("name");
  if (column.type.trim().toLowerCase() !== o.type.trim().toLowerCase()) out.push("type");
  if (column.nullable !== o.nullable) out.push("nullable");
  if (!sameDefault(column.default, o.default)) out.push("default");
  if (column.comment.trim() !== o.comment.trim()) out.push("comment");
  return out;
}

export type ColumnStatus = "new" | "modified" | "deleted" | "unchanged";

export function columnStatus(column: DraftColumn): ColumnStatus {
  if (!column.original) return "new";
  if (column.deleted) return "deleted";
  return changedFields(column).length > 0 ? "modified" : "unchanged";
}

export interface DraftCounts {
  added: number;
  modified: number;
  deleted: number;
}

export function draftCounts(draft: TableDraft): DraftCounts {
  const counts: DraftCounts = { added: 0, modified: 0, deleted: 0 };
  for (const column of draft.columns) {
    const status = columnStatus(column);
    if (status === "new") counts.added++;
    else if (status === "modified") counts.modified++;
    else if (status === "deleted") counts.deleted++;
  }
  return counts;
}

export function tableNameChanged(draft: TableDraft): boolean {
  return draft.name.trim() !== draft.original.name;
}

export function tableCommentChanged(draft: TableDraft): boolean {
  return draft.comment.trim() !== draft.original.comment.trim();
}

/** 草稿与读出的表相比有没有改动。 */
export function isDraftDirty(draft: TableDraft): boolean {
  if (tableNameChanged(draft) || tableCommentChanged(draft)) return true;
  const counts = draftCounts(draft);
  return counts.added + counts.modified + counts.deleted > 0;
}

/**
 * 已有列上锁定的属性:驱动没声明可改的。新增的列不受这些限制,只有驱动根本不能写列注释时注释也锁定
 * (否则注释会被驱动丢掉)。
 */
export function lockedFields(descriptor: DriverDescriptor | undefined, column: DraftColumn): ColumnField[] {
  if (!column.original) return driverCanAlter(descriptor, "column_comment") ? [] : ["comment"];
  return (Object.keys(FIELD_ATTRIBUTE) as (keyof typeof FIELD_ATTRIBUTE)[]).filter(
    (field) => !driverCanAlter(descriptor, FIELD_ATTRIBUTE[field])
  );
}

/** 锁定的属性对应的驱动可改属性,用来生成锁定原因。 */
export function fieldAttribute(field: Exclude<ColumnField, "name">): string {
  return FIELD_ATTRIBUTE[field];
}

/** 驱动能力有限时,它能改与不能改的属性(由声明得出);声明了全部属性时为 null。 */
export function capabilityLimits(descriptor: DriverDescriptor | undefined): { can: string[]; cannot: string[] } | null {
  const can = ALTER_ATTRIBUTES.filter((a) => driverCanAlter(descriptor, a));
  if (can.length === ALTER_ATTRIBUTES.length) return null;
  return { can, cannot: ALTER_ATTRIBUTES.filter((a) => !can.includes(a)) };
}

/**
 * 草稿交给驱动的改表请求:改完之后这张表的样子与要删除的已有列。只发驱动声明了的字段:默认值写法
 * 只在声明了 explicitDefaults 时发,自增只在声明了 autoIncrement 时对新列发;索引改动本轮不发(驱动保留
 * 没提到的索引,自己删掉随列删除的)。
 */
export function buildAlterRequest(
  draft: TableDraft,
  descriptor: DriverDescriptor | undefined
): dbdriver.AlterTableRequest {
  const explicitDefaults = !!descriptor?.explicitDefaults;
  const autoIncrement = !!descriptor?.autoIncrement;
  const columns = draft.columns
    .filter((c) => !c.deleted)
    .map((c) => {
      const column: dbdriver.AlterColumn = {
        originalName: c.original?.name ?? "",
        name: c.name,
        type: c.type,
        nullable: c.nullable,
        defaultValue: defaultText(c.default),
        comment: c.comment,
      };
      // text 是驱动没给写法的原文:不带写法,按旧规则由驱动猜。
      if (explicitDefaults && c.default.kind !== "text") column.defaultKind = c.default.kind;
      if (autoIncrement && !c.original && c.autoIncrement) column.autoIncrement = true;
      return column;
    });
  return dbdriver.AlterTableRequest.createFrom({
    name: draft.name.trim(),
    comment: draft.comment,
    columns,
    drops: draft.columns.filter((c) => c.original && c.deleted).map((c) => c.original!.name),
  });
}

/** 草稿里标记删除的已有列(原名)。 */
export function droppedColumns(draft: TableDraft): string[] {
  return draft.columns.filter((c) => c.original && c.deleted).map((c) => c.original!.name);
}

/** 引用了被删列的已有索引:由草稿得出,不依赖驱动给语句打标。表达式不是列名,不参与匹配。 */
export function indexesDroppedWith(draft: TableDraft, indexes: DesignerIndex[]): string[] {
  const dropped = droppedColumns(draft);
  return indexes
    .filter((index) => index.columns.some((c) => !c.expression && dropped.includes(c.name)))
    .map((index) => index.name);
}

/** 问题指向的位置:表名输入框、某列的某个格子,或整张表的列。 */
export type IssueTarget =
  { kind: "tableName" } | { kind: "column"; key: string; field: "name" | "type" } | { kind: "columns" };

export type IssueCode =
  "tableNameEmpty" | "columnNameEmpty" | "columnNameDuplicate" | "columnTypeEmpty" | "noColumns" | "nameFolding";

/** 校验结果的一条:error 阻止保存,warning 只提醒。row 是表格里的序号(从 1 起)。 */
export interface DraftIssue {
  severity: "error" | "warning";
  code: IssueCode;
  target: IssueTarget;
  row?: number;
  name?: string;
  /** 重名时与之重名的那一列的序号。 */
  duplicateOf?: number;
  /** 名称提醒时驱动声明的折叠规则。 */
  folding?: string;
}

/** 名字与驱动的折叠规则不符:折叠为大写时含小写字母,折叠为小写时含大写字母。 */
function violatesFolding(name: string, folding: string): boolean {
  if (folding === "upper") return name !== name.toUpperCase();
  if (folding === "lower") return name !== name.toLowerCase();
  return false;
}

/**
 * 校验草稿。错误:表名为空、列名为空或重名(不分大小写)、类型为空、没有任何列。提醒:新起或改过的名字
 * 与驱动声明的折叠规则不符(已有的名字保存前后一样,不提醒)。
 */
export function validateDraft(draft: TableDraft, descriptor: DriverDescriptor | undefined): DraftIssue[] {
  const issues: DraftIssue[] = [];
  const folding = descriptor?.nameFolding ?? "";
  const tableName = draft.name.trim();
  if (!tableName) issues.push({ severity: "error", code: "tableNameEmpty", target: { kind: "tableName" } });

  const kept = draft.columns.filter((c) => !c.deleted);
  if (kept.length === 0) issues.push({ severity: "error", code: "noColumns", target: { kind: "columns" } });

  const seen = new Map<string, number>();
  const columnIssues: DraftIssue[] = [];
  const warnings: DraftIssue[] = [];
  draft.columns.forEach((column, i) => {
    if (column.deleted) return;
    const row = i + 1;
    const name = column.name.trim();
    const at = (field: "name" | "type"): IssueTarget => ({ kind: "column", key: column.key, field });
    if (!name) {
      columnIssues.push({ severity: "error", code: "columnNameEmpty", target: at("name"), row, name });
    } else {
      const first = seen.get(name.toLowerCase());
      if (first !== undefined) {
        columnIssues.push({
          severity: "error",
          code: "columnNameDuplicate",
          target: at("name"),
          row,
          name,
          duplicateOf: first,
        });
      } else {
        seen.set(name.toLowerCase(), row);
      }
      const named = !column.original || changedFields(column).includes("name");
      if (named && violatesFolding(name, folding)) {
        warnings.push({ severity: "warning", code: "nameFolding", target: at("name"), row, name, folding });
      }
    }
    if (!column.type.trim()) {
      columnIssues.push({ severity: "error", code: "columnTypeEmpty", target: at("type"), row, name });
    }
  });

  const tableWarnings: DraftIssue[] =
    tableName && tableName !== draft.original.name && violatesFolding(tableName, folding)
      ? [{ severity: "warning", code: "nameFolding", target: { kind: "tableName" }, name: tableName, folding }]
      : [];
  return [...issues, ...columnIssues, ...tableWarnings, ...warnings];
}
