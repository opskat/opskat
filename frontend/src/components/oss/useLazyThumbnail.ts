import { useEffect, type RefObject } from "react";

/** 元素首次进入视口时触发 onEnter 一次然后断开观察。enabled=false 时不观察。 */
export function useLazyThumbnail(ref: RefObject<HTMLElement | null>, enabled: boolean, onEnter: () => void): void {
  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el) return;
    let fired = false;
    const observer = new IntersectionObserver((entries) => {
      if (fired) return;
      if (entries.some((e) => e.isIntersecting)) {
        fired = true;
        onEnter();
        observer.disconnect();
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, enabled, onEnter]);
}
