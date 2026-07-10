import { useEffect, useRef, type RefObject } from "react";

/** 当前资源首次进入视口时触发 onEnter 一次然后断开观察。enabled=false 时不观察。 */
export function useLazyThumbnail(
  ref: RefObject<HTMLElement | null>,
  enabled: boolean,
  resourceKey: string,
  onEnter: () => void
): void {
  // 通过 ref 稳定回调身份：调用方每次渲染都传入新的箭头函数闭包，
  // 若把 onEnter 直接放进下面 effect 的依赖数组，元素仍在视口内时每次重渲染都会
  // 重建 IntersectionObserver 并重新触发一次 onEnter（对预签名失败的图片会导致重复预签名）。
  const onEnterRef = useRef(onEnter);
  useEffect(() => {
    onEnterRef.current = onEnter;
  });

  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el) return;
    let fired = false;
    const observer = new IntersectionObserver((entries) => {
      if (fired) return;
      if (entries.some((e) => e.isIntersecting)) {
        fired = true;
        onEnterRef.current();
        observer.disconnect();
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, enabled, resourceKey]);
}
