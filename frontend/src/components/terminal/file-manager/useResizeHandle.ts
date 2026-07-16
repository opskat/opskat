import { useCallback, useRef, type RefObject } from "react";
import { useResizeHandle as useSharedResizeHandle } from "@opskat/ui";
import { HANDLE_PX } from "./utils";

interface UseResizeHandleOptions {
  onWidthChange: (width: number) => void;
  panelRef: RefObject<HTMLDivElement | null>;
  width: number;
}

export function useResizeHandle({ onWidthChange, panelRef, width }: UseResizeHandleOptions) {
  const outerRef = useRef<HTMLDivElement>(null);
  const previousCursorRef = useRef("");
  const previousTransitionRef = useRef("");

  const handleResizeStartState = useCallback(() => {
    previousCursorRef.current = document.body.style.cursor;
    document.body.style.cursor = "col-resize";
    const outer = outerRef.current;
    previousTransitionRef.current = outer?.style.transition ?? "";
    if (outer) outer.style.transition = "none";
  }, []);
  const handleResize = useCallback((nextWidth: number) => {
    if (outerRef.current) outerRef.current.style.width = `${nextWidth + HANDLE_PX}px`;
  }, []);
  const handleResizeEnd = useCallback(
    (nextWidth: number) => {
      document.body.style.cursor = previousCursorRef.current;
      const outer = outerRef.current;
      if (outer) {
        outer.style.width = "";
        outer.style.transition = previousTransitionRef.current;
      }
      onWidthChange(nextWidth);
    },
    [onWidthChange]
  );
  const { handleMouseDown, isResizing } = useSharedResizeHandle({
    defaultSize: width,
    currentSize: width,
    minSize: Number.MIN_SAFE_INTEGER,
    maxSize: Number.MAX_SAFE_INTEGER,
    reverse: true,
    targetRef: panelRef,
    onResizeStart: handleResizeStartState,
    onResize: handleResize,
    onResizeEnd: handleResizeEnd,
  });

  return { handleResizeStart: handleMouseDown, isResizing, outerRef };
}
