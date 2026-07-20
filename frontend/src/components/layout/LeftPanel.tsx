import { useRef } from "react";
import { useResizeHandle } from "@opskat/ui";
import { useLayoutStore, isCollapsed, MIN_PANEL_WIDTH } from "@/stores/layoutStore";

interface LeftPanelProps {
  children: React.ReactNode;
}

export function LeftPanel({ children }: LeftPanelProps) {
  const width = useLayoutStore((s) => s.leftPanelWidth);
  const setPanelWidth = useLayoutStore((s) => s.setPanelWidth);
  const collapsed = isCollapsed({ leftPanelWidth: width });
  const effectiveWidth = collapsed ? MIN_PANEL_WIDTH : width;
  const panelRef = useRef<HTMLDivElement>(null);
  const { isResizing, handleMouseDown } = useResizeHandle({
    defaultSize: width,
    currentSize: width,
    minSize: MIN_PANEL_WIDTH,
    maxSize: Math.floor(window.innerWidth / 2),
    targetRef: panelRef,
    onResizeEnd: setPanelWidth,
  });

  return (
    <>
      <div ref={panelRef} className="relative shrink-0 overflow-hidden border-r" style={{ width: effectiveWidth }}>
        {children}
        <div
          onMouseDown={handleMouseDown}
          className="absolute right-0 top-0 bottom-0 w-1 cursor-col-resize z-10 hover:bg-primary/20 active:bg-primary/30"
        />
      </div>
      {isResizing && <div className="fixed inset-0 z-50 cursor-col-resize" />}
    </>
  );
}
