import type { TFunction } from "i18next";
import type { dbdriver, query_svc } from "../../../../wailsjs/go/models";
import { driverCanAlter, driverHasCapability, type DriverDescriptor } from "@/lib/dbDrivers";

/**
 * 设计器里一列的默认值。none / null / value / expression 是驱动按写法给出的(声明了默认值写法的驱动,
 * 见 dbdriver.DefaultKind);text 是驱动只给了原文、没说写法(表元数据,或没声明写法的改表模型),原样显示。
 */
export type DesignerDefault =
  | { kind: "none" }
  | { kind: "null" }
  | { kind: "value"; text: string }
  | { kind: "expression"; text: string }
  | { kind: "text"; text: string };

export interface DesignerColumn {
  name: string;
  type: string;
  nullable: boolean;
  primaryKey: boolean;
  autoIncrement: boolean;
  default: DesignerDefault;
  comment: string;
}

/** 索引的一列,同 dbdriver.IndexColumn,缺省字段补齐。 */
export interface DesignerIndexColumn {
  name: string;
  expression: boolean;
  descending: boolean;
  prefixLength: number;
}

/** 索引,同 dbdriver.IndexDefinition,缺省字段补齐;type 是索引类型 id(主键为 primary)。 */
export interface DesignerIndex {
  name: string;
  type: string;
  columns: DesignerIndexColumn[];
  include: string[];
  options: Record<string, unknown>;
  comment: string;
  readOnlyReason: string;
  undroppable: boolean;
}

/** 一条外键:表元数据里逐列的映射按名字合成一条。 */
export interface DesignerForeignKey {
  name: string;
  columns: string[];
  referencedTable: string;
  referencedColumns: string[];
  onUpdate: string;
  onDelete: string;
}

/** 设计器展示的一张表,不论读自改表模型还是表元数据。 */
export interface TableStructure {
  /** 表名;改表模型给的不带 schema,表元数据时是树上的表名。 */
  name: string;
  /** 表注释,没有或读不出时为空串。 */
  comment: string;
  columns: DesignerColumn[];
  indexes: DesignerIndex[];
  foreignKeys: DesignerForeignKey[];
  /** 索引名唯一范围内其他表已有的索引名(改表模型给出时才有)。 */
  takenIndexNames: string[];
  /** 索引读自改表模型(驱动声明了索引编辑),设计器里能改;否则读自表元数据,只能查看。 */
  indexesEditable: boolean;
}

/** 设计器为什么只能查看:只读连接优先于驱动不能改表。 */
export type DesignerReadOnlyReason = "readOnlyConnection" | "driverCannotAlter";

export interface DesignerAccess {
  canAlter: boolean;
  readOnlyReason: DesignerReadOnlyReason | null;
}

/** 按驱动声明与资产的只读连接开关,决定设计器能不能改表结构。 */
export function designerAccess(descriptor: DriverDescriptor | undefined, assetReadOnly: boolean): DesignerAccess {
  if (assetReadOnly) return { canAlter: false, readOnlyReason: "readOnlyConnection" };
  if (!driverHasCapability(descriptor, "alter_table")) return { canAlter: false, readOnlyReason: "driverCannotAlter" };
  return { canAlter: true, readOnlyReason: null };
}

/** 这个资产上能不能新建表:驱动声明了建表,且资产没有开启只读连接。 */
export function canCreateTable(descriptor: DriverDescriptor | undefined, assetReadOnly: boolean): boolean {
  return !assetReadOnly && driverHasCapability(descriptor, "create_table");
}

function indexColumn(c: dbdriver.IndexColumn): DesignerIndexColumn {
  return {
    name: c.name,
    expression: !!c.expression,
    descending: !!c.descending,
    prefixLength: c.prefixLength ?? 0,
  };
}

function indexFromDefinition(d: dbdriver.IndexDefinition): DesignerIndex {
  return {
    name: d.name,
    type: d.type,
    columns: d.columns.map(indexColumn),
    include: d.include ?? [],
    options: d.options ?? {},
    comment: d.comment ?? "",
    readOnlyReason: d.readOnlyReason ?? "",
    undroppable: !!d.undroppable,
  };
}

function sameColumnSet(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((name) => b.includes(name));
}

/** 表元数据的索引:与主键列相同的那个唯一索引是主键,其余按唯一 / 普通。 */
function indexesFromMetadata(metadata: query_svc.TableMetadata): DesignerIndex[] {
  const primaryKey = metadata.columns.filter((c) => c.primaryKey).map((c) => c.name);
  let primaryFound = false;
  return metadata.indexes.map((meta) => {
    let type = meta.unique ? "unique" : "normal";
    if (!primaryFound && meta.unique && primaryKey.length > 0 && sameColumnSet(meta.columns, primaryKey)) {
      type = "primary";
      primaryFound = true;
    }
    return {
      name: meta.name,
      type,
      columns: meta.columns.map((name) => ({ name, expression: false, descending: false, prefixLength: 0 })),
      include: meta.include ?? [],
      options: {},
      comment: "",
      readOnlyReason: "",
      undroppable: false,
    };
  });
}

function groupForeignKeys(rows: query_svc.ForeignKeyMeta[]): DesignerForeignKey[] {
  const out: DesignerForeignKey[] = [];
  for (const row of rows) {
    // 同一条外键的各列映射相邻;名字为空的(驱动读不出名字,如 SQLite)按引用表区分,且复合外键不会重复本表列
    // 或引用列,重复了就是下一条。相邻两条指向同一张表、列都不同的单列外键与一条复合外键仍分不出。
    const last = out[out.length - 1];
    const repeats =
      row.name === "" &&
      !!last &&
      (last.columns.includes(row.column) || last.referencedColumns.includes(row.referencedColumn));
    if (last && !repeats && last.name === row.name && last.referencedTable === row.referencedTable) {
      last.columns.push(row.column);
      last.referencedColumns.push(row.referencedColumn);
      continue;
    }
    out.push({
      name: row.name,
      columns: [row.column],
      referencedTable: row.referencedTable,
      referencedColumns: [row.referencedColumn],
      onUpdate: row.onUpdate ?? "",
      onDelete: row.onDelete ?? "",
    });
  }
  return out;
}

/** 表元数据(Query.GetTableMetadata)映射成设计器的表:没有表注释,索引只能查看。 */
export function structureFromMetadata(table: string, metadata: query_svc.TableMetadata): TableStructure {
  return {
    name: table,
    comment: "",
    columns: metadata.columns.map((c) => ({
      name: c.name,
      type: c.type,
      nullable: c.nullable,
      primaryKey: c.primaryKey,
      autoIncrement: false,
      // 表元数据里 null / 缺省表示没有默认值,与空串区分。
      default: c.default == null ? { kind: "none" } : { kind: "text", text: c.default },
      comment: c.comment,
    })),
    indexes: indexesFromMetadata(metadata),
    foreignKeys: groupForeignKeys(metadata.foreignKeys),
    takenIndexNames: [],
    indexesEditable: false,
  };
}

function defaultOf(c: dbdriver.ColumnDefinition): DesignerDefault {
  switch (c.defaultKind) {
    case "none":
      return { kind: "none" };
    case "null":
      return { kind: "null" };
    case "value":
      return { kind: "value", text: c.defaultValue };
    case "expression":
      return { kind: "expression", text: c.defaultValue };
    default:
      // 驱动没声明默认值写法:空串即没有默认值,其余是原文。
      return c.defaultValue === "" ? { kind: "none" } : { kind: "text", text: c.defaultValue };
  }
}

/**
 * 改表模型(Query.GetAlterTableModel)映射成设计器的表。外键总取自表元数据;模型不带索引(驱动没声明
 * 索引编辑)时,索引与主键也取自表元数据,只能查看。
 */
export function structureFromAlterModel(
  model: dbdriver.AlterTableModel,
  metadata: query_svc.TableMetadata
): TableStructure {
  const modelIndexes = model.indexes;
  const indexes = modelIndexes ? modelIndexes.map(indexFromDefinition) : indexesFromMetadata(metadata);
  const primaryKey = modelIndexes
    ? indexes.filter((i) => i.type === "primary").flatMap((i) => i.columns.map((c) => c.name))
    : metadata.columns.filter((c) => c.primaryKey).map((c) => c.name);
  return {
    name: model.name,
    comment: model.comment,
    columns: model.columns.map((c) => ({
      name: c.name,
      type: c.type,
      nullable: c.nullable,
      primaryKey: primaryKey.includes(c.name),
      autoIncrement: !!c.autoIncrement,
      default: defaultOf(c),
      comment: c.comment,
    })),
    indexes,
    foreignKeys: groupForeignKeys(metadata.foreignKeys),
    takenIndexNames: model.takenIndexNames ?? [],
    indexesEditable: !!modelIndexes,
  };
}

/** 字段表是否有注释列:驱动声明了列注释,或者读出的列里有注释(只能查看结构的驱动也可能读得出注释)。 */
export function showsColumnComments(descriptor: DriverDescriptor | undefined, columns: DesignerColumn[]): boolean {
  return driverCanAlter(descriptor, "column_comment") || columns.some((c) => c.comment !== "");
}

/** 索引表是否有注释列:驱动声明了索引注释,或者读出的索引里有注释。 */
export function showsIndexComments(descriptor: DriverDescriptor | undefined, indexes: DesignerIndex[]): boolean {
  return !!descriptor?.indexEditing?.comments || indexes.some((i) => i.comment !== "");
}

/** 宿主自带文案的索引类型(dbdriver.IndexType 的内置取值);扩展自己的类型由驱动声明名称。 */
const BUILTIN_INDEX_TYPES = ["primary", "unique", "normal", "fulltext"];

/**
 * 驱动声明里的显示名(类型分组、索引类型、索引选项):内置驱动给的是宿主 i18n 键,在这里翻译;扩展驱动的
 * 已由宿主按扩展自己的 locales 翻译成文本,不是键,原样显示。
 */
export function declaredLabel(t: TFunction, label: string): string {
  return t(label, { defaultValue: label });
}

/** 索引类型的显示名:驱动声明的名称,其次宿主对内置类型的文案,都没有时原样。 */
export function indexTypeLabel(descriptor: DriverDescriptor | undefined, type: string, t: TFunction): string {
  const declared = descriptor?.indexEditing?.types.find((d) => d.id === type);
  if (declared) return declaredLabel(t, declared.label);
  if (BUILTIN_INDEX_TYPES.includes(type)) return t(`query.designer.indexType.${type}`);
  return type;
}
