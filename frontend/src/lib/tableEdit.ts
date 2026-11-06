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

function sameRow(a: Record<string, unknown>, b: Record<string, unknown>, primaryKeys: string[]): boolean {
  const keys = primaryKeys.length > 0 ? primaryKeys : Object.keys(a);
  return keys.every((key) => Object.is(a[key], b[key]));
}

/**
 * 已保存的行换成了刷新后的页（previousRows → rows）时，修改跟着它所在的行走：已保存行的修改移到
 * 刷新后同一行（按主键，没有主键时按整行的值）的行号上，新的页里没有那一行时随之丢掉——服务端的
 * 行次序可能变了，按行号留下会把修改落到另一行上；新增行排在刷新后的已保存行之后，值不变。
 */
export function rebaseEdits(
  edits: Map<string, unknown>,
  previousRows: Record<string, unknown>[],
  rows: Record<string, unknown>[],
  primaryKeys: string[]
): Map<string, unknown> {
  if (previousRows === rows || edits.size === 0) return edits;
  const moved = new Map<number, number | undefined>();
  const claimed = new Set<number>();
  const newIndexOf = (rowIdx: number) => {
    if (!moved.has(rowIdx)) {
      const found = rows.findIndex((row, idx) => !claimed.has(idx) && sameRow(previousRows[rowIdx], row, primaryKeys));
      if (found >= 0) claimed.add(found);
      moved.set(rowIdx, found >= 0 ? found : undefined);
    }
    return moved.get(rowIdx);
  };
  const next = new Map<string, unknown>();
  for (const [key, value] of edits) {
    const { rowIdx, column } = parseEditKey(key);
    if (rowIdx >= previousRows.length) {
      next.set(`${rowIdx - previousRows.length + rows.length}:${column}`, value);
      continue;
    }
    const target = newIndexOf(rowIdx);
    if (target !== undefined) next.set(`${target}:${column}`, value);
  }
  return next;
}
