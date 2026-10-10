/** 标签条的滚动几何:标签放不下时横向滚动,这里算"哪一侧还有内容"和"露出某个标签要滚到哪"。 */

interface StripMetrics {
  scrollLeft: number;
  clientWidth: number;
  scrollWidth: number;
}

/** 两侧渐隐区的宽度(其中最外侧一段留给翻页箭头);标签落在渐隐区里也算没露全。 */
export const STRIP_FADE = 40;

/** 哪一侧还有滚出可视区的标签(1px 以内的亚像素误差不算)。 */
export function stripEdges({ scrollLeft, clientWidth, scrollWidth }: StripMetrics): { left: boolean; right: boolean } {
  return {
    left: scrollLeft > 1,
    right: scrollLeft + clientWidth < scrollWidth - 1,
  };
}

/** 让一个标签完整露出(避开渐隐区)所需的 scrollLeft;已经露全则原样返回。 */
export function revealScrollLeft(strip: StripMetrics, tab: { left: number; width: number }): number {
  const max = Math.max(0, strip.scrollWidth - strip.clientWidth);
  const right = tab.left + tab.width;
  let next = strip.scrollLeft;
  if (tab.left < strip.scrollLeft + STRIP_FADE) next = tab.left - STRIP_FADE;
  else if (right > strip.scrollLeft + strip.clientWidth - STRIP_FADE) next = right - strip.clientWidth + STRIP_FADE;
  return Math.min(max, Math.max(0, next));
}
