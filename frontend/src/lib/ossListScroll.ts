/** 抽出滚动到底判定，happy-dom 无布局无法驱动真实 scroll —— 单测这个纯函数，滚动绑定人工验证。 */
export function shouldLoadNextPage(
  scrollTop: number,
  clientHeight: number,
  scrollHeight: number,
  truncated: boolean,
  loadingPage: boolean,
  threshold = 48
): boolean {
  if (!truncated || loadingPage) return false;
  return scrollTop + clientHeight >= scrollHeight - threshold;
}
