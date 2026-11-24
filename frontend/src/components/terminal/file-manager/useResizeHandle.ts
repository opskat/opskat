import { useCallback, useRef, type RefObject } from "react";
import { useResizeHandle as useSharedResizeHandle } from "@opskat/ui";
import { HANDLE_PX } from "./utils";

interface UseResizeHandleOptions {
  onWidthChange: (width: number) => void;
  /** 拖动中每一帧的宽度(此时 width 还没提交),松手时传 null。 */
  onLiveWidth?: (width: number | null) => void;
  panelRef: RefObject<HTMLDivElement | null>;
  width: number;
}

export function useResizeHandle({ onWidthChange, onLiveWidth, panelRef, width }: UseResizeHandleOptions) {
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
  const handleResize = useCallback(
    (nextWidth: number) => {
      if (outerRef.current) outerRef.current.style.width = `${nextWidth + HANDLE_PX}px`;
      onLiveWidth?.(nextWidth);
    },
    [onLiveWidth]
  );
  const handleResizeEnd = useCallback(
    (nextWidth: number) => {
      document.body.style.cursor = previousCursorRef.current;
      const outer = outerRef.current;
      if (outer) {
        outer.style.width = "";
        outer.style.transition = previousTransitionRef.current;
      }
      onWidthChange(nextWidth);
      onLiveWidth?.(null);
    },
    [onLiveWidth, onWidthChange]
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
