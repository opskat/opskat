import { useLayoutEffect, useState, type RefObject } from "react";

/**
 * 元素与紧挨在它前面的兄弟元素共用的宽度：从前一个兄弟的左边缘（没有前一个兄弟时取父容器左边缘）
 * 到父容器右边缘。父容器或前一个兄弟尺寸变化（窗口缩放、左侧栏拖宽）时重新测量。
 */
export function useTrailingSpace(ref: RefObject<HTMLElement | null>): number {
  const [space, setSpace] = useState(() => window.innerWidth);
  useLayoutEffect(() => {
    const parent = ref.current?.parentElement;
    if (!parent) return;
    const previous = ref.current?.previousElementSibling ?? null;
    const measure = () => {
      const start = (previous ?? parent).getBoundingClientRect().left;
      setSpace(parent.getBoundingClientRect().right - start);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(parent);
    if (previous) observer.observe(previous);
    return () => observer.disconnect();
  }, [ref]);
  return space;
}
