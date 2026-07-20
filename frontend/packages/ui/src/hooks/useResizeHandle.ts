import { useState, useCallback, useRef, useEffect } from "react";
import type React from "react";

interface UseResizeHandleOptions {
  defaultSize: number;
  /** Optional externally controlled size. Dragging starts from the latest value. */
  currentSize?: number;
  minSize: number;
  maxSize: number;
  /** "x" for column resize, "y" for row resize. Default "x". */
  axis?: "x" | "y";
  /** true for right/bottom panels where dragging toward origin makes the panel larger */
  reverse?: boolean;
  /** localStorage key — if set, size is persisted across sessions */
  storageKey?: string;
  /** Called when a drag starts. */
  onResizeStart?: () => void;
  /** Called for each clamped drag value; use for linked imperative layout updates. */
  onResize?: (size: number) => void;
  /** Called on drag end with the final size — useful for persisting to a store */
  onResizeEnd?: (size: number) => void;
  /**
   * 可选：拖拽期间不触发 React re-render，改用 rAF 直接写这个元素的 style.width / style.height。
   * 若不传则回退到"每次 mousemove setState"，兼容旧调用方。
   */
  targetRef?: React.RefObject<HTMLElement | null>;
}

function clamp(val: number, min: number, max: number) {
  return Math.max(min, Math.min(max, val));
}

export function useResizeHandle({
  defaultSize,
  currentSize,
  minSize,
  maxSize,
  axis = "x",
  reverse = false,
  storageKey,
  onResizeStart,
  onResize,
  onResizeEnd,
  targetRef,
}: UseResizeHandleOptions) {
  const [size, setSize] = useState(() => {
    if (storageKey) {
      const saved = localStorage.getItem(storageKey);
      if (saved) return clamp(Number(saved), minSize, maxSize);
    }
    return clamp(defaultSize, minSize, maxSize);
  });
  const [isResizing, setIsResizing] = useState(false);
  const sizeRef = useRef(size);
  useEffect(() => {
    sizeRef.current = size;
  }, [size]);
  useEffect(() => {
    if (currentSize != null) sizeRef.current = clamp(currentSize, minSize, maxSize);
  }, [currentSize, minSize, maxSize]);
  // 保持 targetRef 引用在回调内是最新的，而不用把它放进 handleMouseDown 依赖里
  const targetRefRef = useRef(targetRef);
  useEffect(() => {
    targetRefRef.current = targetRef;
  }, [targetRef]);

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setIsResizing(true);
      onResizeStart?.();
      const start = axis === "x" ? e.clientX : e.clientY;
      const startSize = currentSize == null ? sizeRef.current : clamp(currentSize, minSize, maxSize);
      const target = targetRefRef.current?.current ?? null;
      const styleProp = axis === "x" ? "width" : "height";

      let pending = startSize;
      let rafId: number | null = null;

      const flushToDom = () => {
        rafId = null;
        if (target) target.style[styleProp] = `${pending}px`;
      };

      const onMouseMove = (ev: MouseEvent) => {
        const current = axis === "x" ? ev.clientX : ev.clientY;
        const delta = reverse ? start - current : current - start;
        const next = clamp(startSize + delta, minSize, maxSize);
        pending = next;
        onResize?.(next);
        if (target) {
          // 走 DOM + rAF：React 不参与
          if (rafId == null) rafId = requestAnimationFrame(flushToDom);
        } else {
          // 兼容老调用方：仍然 setState
          setSize(next);
        }
      };

      const onMouseUp = () => {
        if (rafId != null) {
          cancelAnimationFrame(rafId);
          rafId = null;
        }
        setIsResizing(false);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        // 拖拽结束，把最终值同步进 state —— 取消拖拽中 DOM 的 inline 覆盖，让 React 重新掌管
        if (target) {
          target.style[styleProp] = "";
          setSize(pending);
        }
        if (storageKey) {
          localStorage.setItem(storageKey, String(pending));
        }
        onResizeEnd?.(pending);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [currentSize, minSize, maxSize, axis, reverse, storageKey, onResizeStart, onResize, onResizeEnd]
  );

  return {
    size: currentSize == null ? size : clamp(currentSize, minSize, maxSize),
    isResizing,
    handleMouseDown,
  };
}
