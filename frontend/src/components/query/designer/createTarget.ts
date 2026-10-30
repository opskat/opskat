// 新建表建在哪里:库,与有 schema 的驱动里的 schema(空表示驱动的默认 schema)。

export interface CreateTableTarget {
  database: string;
  schema?: string;
}

/** 有 schema 的驱动在树上把表列为 schema.表:取出 schema;没有点时为空串。 */
export function schemaOfTable(table: string): string {
  const dot = table.indexOf(".");
  return dot > 0 ? table.slice(0, dot) : "";
}

/** 一个库里已知的 schema(从树上列出的表得出,按出现次序去重)。 */
export function knownSchemas(tables: string[] | undefined): string[] {
  return [...new Set((tables ?? []).map(schemaOfTable).filter((s) => s !== ""))];
}

/**
 * 树上某张表所在的位置作为新建表的目标:库,有 schema 的驱动再带上表所在的 schema。
 */
export function targetOfTable(database: string, table: string, schemas: boolean): CreateTableTarget {
  const schema = schemas ? schemaOfTable(table) : "";
  return schema ? { database, schema } : { database };
}

/**
 * 没有具体位置的入口(侧栏工具栏的新建、空白区的按钮)的目标:选中的树节点所在的库(与 schema),没有选中时
 * 取树上第一个展开的库,再没有时取资产的默认库;都没有时为 null(入口不可用)。
 */
export function defaultCreateTarget(opts: {
  selected?: { database: string; table: string };
  databases: string[];
  expandedDbs: string[];
  defaultDatabase: string;
  schemas: boolean;
}): CreateTableTarget | null {
  if (opts.selected) return targetOfTable(opts.selected.database, opts.selected.table, opts.schemas);
  const expanded = opts.databases.find((db) => opts.expandedDbs.includes(db));
  if (expanded) return { database: expanded };
  return opts.defaultDatabase ? { database: opts.defaultDatabase } : null;
}

/** 目标的显示名:库,或 库.schema。 */
export function targetLabel(target: CreateTableTarget): string {
  return target.schema ? `${target.database}.${target.schema}` : target.database;
}
