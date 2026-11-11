import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
  window.addEventListener("resize", onChange);
  return () => window.removeEventListener("resize", onChange);
}

function getWidth() {
  return window.innerWidth;
}

/** 当前窗口宽度，窗口缩放时触发重渲染。 */
export function useWindowWidth(): number {
  return useSyncExternalStore(subscribe, getWidth);
}
