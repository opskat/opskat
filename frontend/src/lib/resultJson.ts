/**
 * 把结果行序列化成展示用 JSON。
 *
 * 刻意不走 cellValueToDisplayText:网格为了排版把单元格文本截断到 CELL_DISPLAY_MAX_CHARS,
 * 而用户切到 JSON 正是为了读那些长值 —— 继承截断等于让这个视图失去存在意义。
 *
 * `edits` 是 TableDataTab 的未提交编辑,键为 `${rowIdx}:${column}`,与网格显示保持一致。
 */
export function resultRowsToJson(
  rows: Record<string, unknown>[],
  columns: string[],
  edits?: Map<string, unknown>
): string {
  const documents = rows.map((row, rowIdx) => {
    const document: Record<string, unknown> = {};
    for (const column of columns) {
      const key = `${rowIdx}:${column}`;
      document[column] = edits?.has(key) ? edits.get(key) : row[column];
    }
    return document;
  });
  return JSON.stringify(documents, null, 2);
}
