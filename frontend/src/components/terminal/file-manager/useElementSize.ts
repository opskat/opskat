import { useEffect, useRef, useState, type RefObject } from "react";

export interface ElementSize {
  width: number;
  height: number;
}

/**
 * 用 ResizeObserver 跟着元素盒子实测尺寸(面板可拖宽、窗口可缩放,父组件手里的宽高状态
 * 不一定是这个盒子的真实尺寸)。未测出前返回 fallback;测得 0 的维度保留上一次的值 ——
 * 面板收起动画途中盒子会短暂塌成 0,不该让依赖它的布局跟着跳。
 */
export function useElementSize<T extends HTMLElement>(fallback: ElementSize): [RefObject<T | null>, ElementSize] {
  const ref = useRef<T | null>(null);
  const [size, setSize] = useState(fallback);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      setSize((prev) => {
        const width = rect.width > 0 ? rect.width : prev.width;
        const height = rect.height > 0 ? rect.height : prev.height;
        return width === prev.width && height === prev.height ? prev : { width, height };
      });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, size];
}
