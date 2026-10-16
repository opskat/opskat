// 表格编辑的意图：表格里的修改（按 "行号:列名" 记，新增行排在已保存的行之后）怎么整理成发给驱动的
// 请求，提交后怎么留下没成功的修改。语句本身由驱动生成（Query.GetGridEditStatements）。

export interface TableColumnRule {
  name: string;
  nullable: boolean;
  hasDefault: boolean;
  autoIncrement?: boolean;
}

function hasOwnValue(values: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(values, key);
}

function isMissingRequiredValue(value: unknown): boolean {
  return value == null || value === "";
}

export function validateInsertRow(rules: TableColumnRule[], values: Record<string, unknown>): string[] {
  return rules
    .filter((rule) => !rule.nullable && !rule.hasDefault && !rule.autoIncrement)
    .filter((rule) => !hasOwnValue(values, rule.name) || isMissingRequiredValue(values[rule.name]))
    .map((rule) => rule.name);
}

export interface ColumnValue {
  column: string;
  value: unknown;
}

/** 发给驱动的表格编辑请求（与后端 dbdriver.GridEditRequest 对应）。 */
export interface GridEditIntent {
  columns: string[];
  primaryKeys: string[];
  inserts: { values: ColumnValue[] }[];
  updates: { row: Record<string, unknown>; set: ColumnValue[] }[];
  deletes: Record<string, unknown>[];
}

export interface PendingGridEdits {
  intent: GridEditIntent;
  /** intent.inserts / intent.updates 各自对应的表格行号，驱动给的语句按同样的次序一一对应。 */
  insertRowIdxs: number[];
  updateRowIdxs: number[];
  /** 新增行里没填的必填列；非空时不能提交。 */
  missingFields: string[];
}

function parseEditKey(key: string): { rowIdx: number; column: string } {
  const sep = key.indexOf(":");
  return { rowIdx: Number(key.substring(0, sep)), column: key.substring(sep + 1) };
}

/**
 * 把表格里的修改整理成发给驱动的请求：新增行按表格次序、只带填写过的列（按列的次序）；改动的已有行
 * 按第一次修改的先后、带原行（定位用）与改过的列（按修改的先后）。
 */
export function collectGridEdits({
  rows,
  newRowCount,
  columns,
  primaryKeys,
  columnRules,
  edits,
}: {
  rows: Record<string, unknown>[];
  newRowCount: number;
  columns: string[];
  primaryKeys: string[];
  columnRules: TableColumnRule[];
  edits: Map<string, unknown>;
}): PendingGridEdits {
  const intent: GridEditIntent = { columns, primaryKeys, inserts: [], updates: [], deletes: [] };
  const insertRowIdxs: number[] = [];
  const missing = new Set<string>();
  for (let newRowIdx = 0; newRowIdx < newRowCount; newRowIdx++) {
    const rowIdx = rows.length + newRowIdx;
    const values: Record<string, unknown> = {};
    for (const column of columns) {
      const key = `${rowIdx}:${column}`;
      if (edits.has(key)) values[column] = edits.get(key);
    }
    for (const field of validateInsertRow(columnRules, values)) missing.add(field);
    intent.inserts.push({ values: Object.entries(values).map(([column, value]) => ({ column, value })) });
    insertRowIdxs.push(rowIdx);
  }

  const setByRow = new Map<number, ColumnValue[]>();
  for (const [key, value] of edits) {
    const { rowIdx, column } = parseEditKey(key);
    if (rowIdx >= rows.length) continue;
    const set = setByRow.get(rowIdx) ?? [];
    set.push({ column, value });
    setByRow.set(rowIdx, set);
  }
  const updateRowIdxs = [...setByRow.keys()];
  for (const rowIdx of updateRowIdxs) intent.updates.push({ row: rows[rowIdx], set: setByRow.get(rowIdx)! });

  return { intent, insertRowIdxs, updateRowIdxs, missingFields: [...missing] };
}

/**
 * 从修改里去掉一些行：已保存行只丢掉它的修改；新增行连同它的值移除，排在它后面的新增行前移。
 * rowsLength 是这些行号所基于的已保存行数。
 */
export function dropRowEdits(
  edits: Map<string, unknown>,
  rowsLength: number,
  dropped: ReadonlySet<number>
): Map<string, unknown> {
  const droppedNewRows = [...dropped].filter((rowIdx) => rowIdx >= rowsLength);
  const next = new Map<string, unknown>();
  for (const [key, value] of edits) {
    const { rowIdx, column } = parseEditKey(key);
    if (dropped.has(rowIdx)) continue;
    const shift = rowIdx >= rowsLength ? droppedNewRows.filter((idx) => idx < rowIdx).length : 0;
    next.set(`${rowIdx - shift}:${column}`, value);
  }
  return next;
}

/**
 * 已保存的行数变了（刷新后的页更长或更短）时，新增行的行号跟着移动，值仍在原来那些新增行上。
 * 已保存行的修改行号不变；超出新页的已保存行已不在表格里，它的修改随之丢掉。
 */
export function rebaseNewRowEdits(
  edits: Map<string, unknown>,
  previousRowsLength: number,
  rowsLength: number
): Map<string, unknown> {
  if (previousRowsLength === rowsLength) return edits;
  const next = new Map<string, unknown>();
  for (const [key, value] of edits) {
    const { rowIdx, column } = parseEditKey(key);
    if (rowIdx >= previousRowsLength) next.set(`${rowIdx - previousRowsLength + rowsLength}:${column}`, value);
    else if (rowIdx < rowsLength) next.set(key, value);
  }
  return next;
}
