// SQL 上下文解析 + 补全项构建（纯函数，无 Monaco / React 依赖，便于单测）。
//
// SqlEditorTab 在 monaco 补全回调里调用 buildSqlCompletions，得到"表 + 字段"两类动态项，
// 再映射成 monaco 的 CompletionItem（关键字 / 函数由 monaco-completions.ts 的 provider 统一附加）。
//
// 字段联想依赖调用方把已加载的表结构（GetTableMetadata 的 columns）按"表名 -> 列"传进来，
// 表名的规范形式与 queryStore.dbStates[tab].tables[db] 中的条目一致
// （MySQL/SQLite 为裸表名，PostgreSQL/MSSQL 为 schema.table）。

export interface TableRef {
  table: string;
  alias?: string;
}

export interface SqlColumn {
  name: string;
  type?: string;
  nullable?: boolean;
  primaryKey?: boolean;
}

export interface SqlCompletionItem {
  label: string;
  kind: "table" | "column";
  insertText: string;
  detail?: string;
  sortText: string;
}

export interface BuildSqlCompletionsArgs {
  /** 光标所在行、光标之前的文本，用于判断是否处于 `xxx.` 之后。 */
  textBeforeCursor: string;
  /** 整段 SQL，用于解析 FROM / JOIN 中在作用域内的表。 */
  fullSql: string;
  /** 当前库下已知的表（规范表名，与 queryStore 中的列表一致）。 */
  tables: string[];
  /** 当前库名，仅用于表项的 detail 展示。 */
  db: string;
  /** 规范表名 -> 列，由调用方从已缓存的表结构组装。 */
  columnsByTable: Record<string, SqlColumn[]>;
}

// 允许出现在表名后、但不能被当作别名的关键字。
const RESERVED_ALIAS = new Set([
  "on",
  "where",
  "group",
  "order",
  "having",
  "limit",
  "offset",
  "join",
  "inner",
  "left",
  "right",
  "outer",
  "cross",
  "full",
  "union",
  "set",
  "using",
  "as",
  "and",
  "or",
  "natural",
  "lateral",
  "fetch",
  "for",
  "window",
  "returning",
  "values",
  "into",
  "select",
  "from",
]);

// 可带 schema 前缀、可被 ` " [] 包裹的标识符。
const IDENT = '[`"\\[]?[\\w$]+[`"\\]]?(?:\\.[`"\\[]?[\\w$]+[`"\\]]?)*';

// FROM 列表在遇到这些关键字时结束（其后是 JOIN / WHERE 等子句）。
const FROM_TERMINATORS =
  "where|group|order|having|limit|offset|union|join|inner|left|right|outer|cross|full|natural|lateral|on";

/** 去除标识符各段外层的 ` " [] 引号。 */
function stripQuotes(id: string): string {
  return id
    .split(".")
    .map((seg) => seg.replace(/^[`"[]/, "").replace(/[`"\]]$/, ""))
    .join(".");
}

/** 取标识符最后一段（去 schema 前缀），用作展示 owner。 */
function shortName(table: string): string {
  const t = stripQuotes(table);
  const dot = t.lastIndexOf(".");
  return dot >= 0 ? t.slice(dot + 1) : t;
}

function makeRef(table: string, alias: string | undefined): TableRef {
  return alias ? { table, alias } : { table };
}

/** 从一个表来源片段（如 "users u" / "public.orders AS o"）解析出表名与可选别名。 */
function parseFragment(fragment: string): TableRef | null {
  const m = new RegExp(`^\\s*(${IDENT})(?:\\s+(?:as\\s+)?([\\w$]+))?`, "i").exec(fragment);
  if (!m) return null;
  const table = stripQuotes(m[1]);
  const alias = m[2] && !RESERVED_ALIAS.has(m[2].toLowerCase()) ? m[2] : undefined;
  return makeRef(table, alias);
}

/** 解析 SQL 中 FROM / JOIN 引用的表（含别名）。尽力而为的轻量解析，覆盖常见写法。 */
export function parseTableRefs(sql: string): TableRef[] {
  const refs: TableRef[] = [];

  // FROM 列表（逗号分隔），到下一个子句关键字为止。
  const fromRe = new RegExp(`\\bfrom\\s+([\\s\\S]*?)(?=\\b(?:${FROM_TERMINATORS})\\b|;|$)`, "gi");
  for (let m = fromRe.exec(sql); m; m = fromRe.exec(sql)) {
    for (const fragment of m[1].split(",")) {
      const ref = parseFragment(fragment);
      if (ref) refs.push(ref);
    }
  }

  // 各 JOIN 子句：JOIN <table> [alias] [ON ...]
  const joinRe = new RegExp(`\\bjoin\\s+(${IDENT})(?:\\s+(?:as\\s+)?([\\w$]+))?`, "gi");
  for (let m = joinRe.exec(sql); m; m = joinRe.exec(sql)) {
    const table = stripQuotes(m[1]);
    const alias = m[2] && !RESERVED_ALIAS.has(m[2].toLowerCase()) ? m[2] : undefined;
    refs.push(makeRef(table, alias));
  }

  return refs;
}

/** 若光标正处于 `ident.` 之后，返回该点号前的标识符（别名或表名的末段），否则返回 null。 */
export function dotPrefixAt(textBeforeCursor: string): string | null {
  const m = /([\w$]+)\.[\w$]*$/.exec(textBeforeCursor);
  return m ? m[1] : null;
}

/** 把一个解析出的表名解析为已知表列表中的规范表名，匹配不到返回 null。 */
export function resolveTableName(ref: string, tables: string[]): string | null {
  const norm = stripQuotes(ref).toLowerCase();
  for (const t of tables) {
    if (stripQuotes(t).toLowerCase() === norm) return t;
  }
  // 未带 schema 的引用：按末段匹配（如 users -> public.users）
  if (!norm.includes(".")) {
    for (const t of tables) {
      const tl = stripQuotes(t).toLowerCase();
      if (tl.slice(tl.lastIndexOf(".") + 1) === norm) return t;
    }
  }
  return null;
}

/** 解析 SQL 中 FROM/JOIN 引用、且能对应到已知表列表的规范表名（去重）。用于按需预取表结构。 */
export function resolveScopeTables(sql: string, tables: string[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const ref of parseTableRefs(sql)) {
    const canonical = resolveTableName(ref.table, tables);
    if (canonical && !seen.has(canonical)) {
      seen.add(canonical);
      out.push(canonical);
    }
  }
  return out;
}

function columnItem(col: SqlColumn, owner: string): SqlCompletionItem {
  const pk = col.primaryKey ? " 🔑" : "";
  const detail = `${col.type ?? ""}${pk} · ${owner}`.trim();
  return { label: col.name, kind: "column", insertText: col.name, detail, sortText: "00_" + col.name };
}

function tableItem(table: string, db: string): SqlCompletionItem {
  return {
    label: table,
    kind: "table",
    insertText: table,
    detail: db ? `table · ${db}` : "table",
    sortText: "01_" + table,
  };
}

/**
 * 构建 SQL 动态补全项：
 * - 光标在 `别名.` / `表名.` 之后 → 仅该表的字段；点号前无法解析成已知表时返回空。
 * - 其余（裸标识符）→ 当前 FROM/JOIN 作用域内所有表的字段 + 库中所有表，字段排在表之前。
 */
export function buildSqlCompletions({
  textBeforeCursor,
  fullSql,
  tables,
  db,
  columnsByTable,
}: BuildSqlCompletionsArgs): SqlCompletionItem[] {
  const refs = parseTableRefs(fullSql);
  const dot = dotPrefixAt(textBeforeCursor);

  if (dot !== null) {
    const dotLower = dot.toLowerCase();
    const aliasRef = refs.find((r) => r.alias?.toLowerCase() === dotLower);
    let canonical: string | null;
    let owner: string;
    if (aliasRef) {
      canonical = resolveTableName(aliasRef.table, tables);
      owner = aliasRef.alias!;
    } else {
      canonical = resolveTableName(dot, tables);
      owner = canonical ? shortName(canonical) : dot;
    }
    if (!canonical) return [];
    return (columnsByTable[canonical] ?? []).map((c) => columnItem(c, owner));
  }

  const items: SqlCompletionItem[] = [];
  const seen = new Set<string>();
  for (const ref of refs) {
    const canonical = resolveTableName(ref.table, tables);
    if (!canonical) continue;
    const key = canonical + "|" + (ref.alias ?? "");
    if (seen.has(key)) continue;
    seen.add(key);
    const owner = ref.alias ?? shortName(canonical);
    for (const c of columnsByTable[canonical] ?? []) items.push(columnItem(c, owner));
  }
  for (const t of tables) items.push(tableItem(t, db));
  return items;
}
