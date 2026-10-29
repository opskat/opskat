import { dbdriver } from "../../../../wailsjs/go/models";
import { driverCanAlter, type DriverDescriptor } from "@/lib/dbDrivers";
import type { DesignerColumn, DesignerDefault, TableStructure } from "./model";
import {
  alterIndexes,
  existingColumnKey,
  existingIndex,
  indexCounts,
  rebaseIndexes,
  removeColumnRefs,
  syncAutoNames,
  validateIndexes,
  violatesFolding,
  type DraftIndex,
  type IndexField,
} from "./indexDraft";

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

/** 设计器的草稿:表名、表注释、各列与各索引(已有的按读出的次序在前,新增的在后)。 */
export interface TableDraft {
  /** 读出时的表名与表注释。 */
  original: { name: string; comment: string };
  name: string;
  comment: string;
  columns: DraftColumn[];
  indexes: DraftIndex[];
  /** 索引名唯一范围内其他表已有的索引名(重名校验用,见 TableStructure.takenIndexNames)。 */
  takenIndexNames: string[];
  /** 下一个新增列 / 索引的编号,用来生成 key。 */
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
    columns: structure.columns.map(existingColumn),
    indexes: structure.indexes.map(existingIndex),
    takenIndexNames: structure.takenIndexNames,
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

/**
 * 改一列;删除新增的列是直接移除(索引里对它的引用一起拿掉),删除已有列只做标记(引用它的索引随列删除,
 * 由草稿得出)。列名变了时新索引的自动名字跟着变。
 */
export function updateColumn(draft: TableDraft, key: string, patch: DraftColumnPatch): TableDraft {
  const target = draft.columns.find((c) => c.key === key);
  if (!target) return draft;
  if (patch.deleted && !target.original) {
    return syncAutoNames({
      ...draft,
      columns: draft.columns.filter((c) => c.key !== key),
      indexes: removeColumnRefs(draft.indexes, key),
    });
  }
  return syncAutoNames({ ...draft, columns: draft.columns.map((c) => (c.key === key ? { ...c, ...patch } : c)) });
}

/** 把一列的某个属性还原成读出时的值(只对已有列有意义)。 */
export function revertField(column: DraftColumn, field: ColumnField): DraftColumn {
  if (!column.original) return column;
  return { ...column, [field]: column.original[field] };
}

export function defaultText(value: DesignerDefault): string {
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

/** 字段的改动计数(索引的见 indexCounts)。 */
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
  return [draftCounts(draft), indexCounts(draft)].some((c) => c.added + c.modified + c.deleted > 0);
}

/**
 * 已有列上锁定的属性:驱动没声明可改的。新增的列不受这些限制,只有驱动写不了它的注释时注释也锁定(否则
 * 注释会被驱动丢掉):新建表(creating)看驱动是否声明了建表注释,已有表的新列看能否改列注释。
 */
export function lockedFields(
  descriptor: DriverDescriptor | undefined,
  column: DraftColumn,
  creating = false
): ColumnField[] {
  if (!column.original) return writesNewColumnComments(descriptor, creating) ? [] : ["comment"];
  return (Object.keys(FIELD_ATTRIBUTE) as (keyof typeof FIELD_ATTRIBUTE)[]).filter(
    (field) => !driverCanAlter(descriptor, FIELD_ATTRIBUTE[field])
  );
}

/** 新列的注释写不写得出:新建表要驱动声明建表注释,已有表的新列要驱动能改列注释。 */
export function writesNewColumnComments(descriptor: DriverDescriptor | undefined, creating: boolean): boolean {
  return creating ? !!descriptor?.createTableComments : driverCanAlter(descriptor, "column_comment");
}

/**
 * 一列的自增能不能勾:驱动声明了自增,且是新列;驱动声明自增只能在新表上设时,已有表的新列也不行,
 * 新建表(creating)的列可以。已有列的自增不能改,只显示现状。
 */
export function autoIncrementEditable(
  descriptor: DriverDescriptor | undefined,
  column: DraftColumn,
  creating: boolean
): boolean {
  if (!descriptor?.autoIncrement || column.original) return false;
  return creating || !descriptor.autoIncrementNewTablesOnly;
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
 * 草稿交给驱动的改表请求:改完之后这张表的样子与要删除的已有列、索引。只发驱动声明了的字段:默认值写法
 * 只在声明了 explicitDefaults 时发,自增只在声明了 autoIncrement 时对新列发,索引只在声明了 indexEditing
 * 时发(见 alterIndexes;没声明时驱动保留全部索引,随列删除的由数据库丢掉)。
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
    ...(descriptor?.indexEditing ? alterIndexes(draft) : {}),
  });
}

function existingColumn(column: DesignerColumn): DraftColumn {
  return { key: existingColumnKey(column.name), original: column, deleted: false, ...column };
}

/**
 * 把草稿的目标状态搬到重新读出的表上(逐条执行停在中途后用):已生效的改动不再标记,没执行的留在草稿里。
 * 已有列按原名找,原名不在了再按目标名找(改名已执行);找不到的列(删除已执行)从草稿里去掉。新增的列
 * 在读出的表里有同名列时说明加列已执行,按读出的样子成为已有列。读出的表里草稿没有的列按读出的样子列入。
 * 次序同 initDraft:读出的列在前,还没加上的新列在后。索引同样搬过去(见 rebaseIndexes),对列的引用换成
 * 搬过之后的列。
 */
export function rebaseDraft(draft: TableDraft, structure: TableStructure): TableDraft {
  const byName = new Map(structure.columns.map((c) => [c.name, c]));
  const claimed = new Map<string, DraftColumn>();
  const pending: DraftColumn[] = [];
  // 草稿列的 key 到搬过之后的 key;删除已执行的列不在其中。
  const columnKeys = new Map<string, string>();
  for (const column of draft.columns) {
    const target = column.name.trim();
    if (!column.original) {
      const added = byName.get(target);
      if (added && !claimed.has(added.name)) {
        claimed.set(added.name, existingColumn(added));
        columnKeys.set(column.key, existingColumnKey(added.name));
      } else {
        pending.push(column);
        columnKeys.set(column.key, column.key);
      }
      continue;
    }
    const current = byName.get(column.original.name) ?? byName.get(target);
    if (!current || claimed.has(current.name)) continue;
    claimed.set(current.name, {
      ...column,
      key: existingColumnKey(current.name),
      original: current,
      primaryKey: current.primaryKey,
      autoIncrement: current.autoIncrement,
    });
    columnKeys.set(column.key, existingColumnKey(current.name));
  }
  const rebased: TableDraft = {
    ...draft,
    original: { name: structure.name, comment: structure.comment },
    columns: [...structure.columns.map((c) => claimed.get(c.name) ?? existingColumn(c)), ...pending],
    takenIndexNames: structure.takenIndexNames,
  };
  return { ...rebased, ...rebaseIndexes(rebased, structure.indexes, columnKeys) };
}

/** 草稿里标记删除的已有列(原名)。 */
export function droppedColumns(draft: TableDraft): string[] {
  return draft.columns.filter((c) => c.original && c.deleted).map((c) => c.original!.name);
}

/** 问题指向的位置:表名输入框、某列的某个格子、整张表的列,或某个索引的某一项。 */
export type IssueTarget =
  | { kind: "tableName" }
  | { kind: "column"; key: string; field: "name" | "type" | "default" }
  | { kind: "columns" }
  | { kind: "index"; key: string; field: IndexField };

export type IssueCode =
  | "tableNameEmpty"
  | "columnNameEmpty"
  | "columnNameDuplicate"
  | "columnTypeEmpty"
  | "noColumns"
  | "nameFolding"
  | "emptyStringIsNull"
  | "indexNameEmpty"
  | "indexNameDuplicate"
  | "indexNameTaken"
  | "indexColumnsEmpty"
  | "primaryKeyDuplicate"
  | "indexOptionExclusive";

/** 校验结果的一条:error 阻止保存,warning 只提醒。row 是字段表或索引表里的序号(从 1 起)。 */
export interface DraftIssue {
  severity: "error" | "warning";
  code: IssueCode;
  target: IssueTarget;
  row?: number;
  name?: string;
  /** 重名时与之重名的那一列 / 那个索引的序号。 */
  duplicateOf?: number;
  /** 名称提醒时驱动声明的折叠规则。 */
  folding?: string;
  /** 独占选项冲突时选项的显示名(驱动声明的,见 declaredLabel)。 */
  option?: string;
}

/**
 * 校验草稿。错误:表名为空、列名为空或重名(不分大小写)、类型为空、没有任何列,以及索引的问题(见
 * validateIndexes)。提醒:新起或改过的名字与驱动声明的折叠规则不符(已有的名字保存前后一样,不提醒);驱动声明
 * 空字符串即 NULL 时,新设或改过的默认值是空串的「值」。
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
    const at = (field: "name" | "type" | "default"): IssueTarget => ({ kind: "column", key: column.key, field });
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
    const defaultSet = !column.original || changedFields(column).includes("default");
    if (descriptor?.emptyStringIsNull && defaultSet && column.default.kind === "value" && column.default.text === "") {
      warnings.push({ severity: "warning", code: "emptyStringIsNull", target: at("default"), row, name });
    }
  });

  const tableWarnings: DraftIssue[] =
    tableName && tableName !== draft.original.name && violatesFolding(tableName, folding)
      ? [{ severity: "warning", code: "nameFolding", target: { kind: "tableName" }, name: tableName, folding }]
      : [];
  const indexIssues = validateIndexes(draft, descriptor);
  return [...issues, ...columnIssues, ...indexIssues.errors, ...tableWarnings, ...warnings, ...indexIssues.warnings];
}
