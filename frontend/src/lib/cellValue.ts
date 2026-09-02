export function cellValueToText(v: unknown): string {
  if (v == null) return "";
  if (typeof v === "object") {
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }
  return String(v);
}

// 单元格放进 DOM 的最大字符数。表格单元格用的是 `truncate`(nowrap + ellipsis),
// Chromium 为了排出这一行的 line box 必须把整段文本 shape 完,代价与值长度成正比 ——
// 哪怕最终只有 ~150px 可见。实测(45 个可见单元格,Chromium):
//   10KB/单元格 → 20ms layout,100KB → 195ms,1MB → 2.3s;之后每次重排(改列宽 /
//   窗口缩放 / measureElement 的 ResizeObserver)1MB 仍要 163ms。截断后恒定 ~4ms。
// 512 字符远超单列最大宽度 420px 能显示的量(12px 等宽字体约 60 字符),所以看得见的
// 内容一个都没少。完整值仍然走复制 / 编辑 / 导出路径,不受这里影响。
export const CELL_DISPLAY_MAX_CHARS = 512;

/** 面向渲染的文本:与 cellValueToText 相同,但截断到 CELL_DISPLAY_MAX_CHARS。 */
export function cellValueToDisplayText(v: unknown): string {
  const text = cellValueToText(v);
  return text.length > CELL_DISPLAY_MAX_CHARS ? `${text.slice(0, CELL_DISPLAY_MAX_CHARS)}…` : text;
}
