import { useRef, useCallback, useState } from "react";
import { Loader2 } from "lucide-react";
import { Terminal } from "./Terminal";
import { ConnectionProgress } from "./ConnectionProgress";
import { useTerminalStore, type SplitNode } from "@/stores/terminalStore";

interface SplitPaneProps {
  node: SplitNode;
  tabId: string;
  isTabActive: boolean;
  activePaneId: string;
  showFocusRing: boolean;
  path: number[];
}

export function SplitPane({ node, tabId, isTabActive, activePaneId, showFocusRing, path }: SplitPaneProps) {
  if (node.type === "terminal") {
    const isFocused = activePaneId === node.sessionId;
    return (
      <div
        className="h-full w-full relative"
        onMouseDown={() => {
          if (!isFocused) {
            useTerminalStore.getState().setActivePaneId(tabId, node.sessionId);
          }
        }}
      >
        {showFocusRing && isFocused && (
          <div className="absolute inset-0 ring-1 ring-primary/40 rounded-sm pointer-events-none z-10" />
        )}
        <Terminal sessionId={node.sessionId} active={isTabActive} tabId={tabId} />
      </div>
    );
  }

  if (node.type === "pending") {
    return (
      <div className="h-full w-full flex items-center justify-center bg-background">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (node.type === "connecting") {
    return <ConnectionProgress connectionId={node.connectionId} />;
  }

  return (
    <SplitContainer
      node={node}
      tabId={tabId}
      isTabActive={isTabActive}
      activePaneId={activePaneId}
      showFocusRing={showFocusRing}
      path={path}
    />
  );
}

// Separate component to use hooks
function SplitContainer({
  node,
  tabId,
  isTabActive,
  activePaneId,
  showFocusRing,
  path,
}: Omit<SplitPaneProps, "node"> & {
  node: Extract<SplitNode, { type: "split" }>;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [isDragging, setIsDragging] = useState(false);
  const isVertical = node.direction === "vertical";

  const handleDragStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const container = containerRef.current;
      if (!container) return;

      setIsDragging(true);
      const rect = container.getBoundingClientRect();

      const onMouseMove = (e: MouseEvent) => {
        let ratio: number;
        if (isVertical) {
          ratio = (e.clientX - rect.left) / rect.width;
        } else {
          ratio = (e.clientY - rect.top) / rect.height;
        }
        ratio = Math.max(0.1, Math.min(0.9, ratio));
        useTerminalStore.getState().setSplitRatio(tabId, path, ratio);
      };

      const onMouseUp = () => {
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        setIsDragging(false);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
      document.body.style.cursor = isVertical ? "col-resize" : "row-resize";
      document.body.style.userSelect = "none";
    },
    [tabId, path, isVertical]
  );

  const transition = isDragging ? "none" : "flex 150ms ease-out";

  return (
    <div ref={containerRef} className={`flex h-full w-full ${isVertical ? "flex-row" : "flex-col"}`}>
      <div className="overflow-hidden min-w-0 min-h-0" style={{ flex: node.ratio, transition }}>
        <SplitPane
          node={node.first}
          tabId={tabId}
          isTabActive={isTabActive}
          activePaneId={activePaneId}
          showFocusRing={showFocusRing}
          path={[...path, 0]}
        />
      </div>
      <div
        className={`shrink-0 bg-border hover:bg-primary/50 transition-colors ${
          isVertical ? "w-1 cursor-col-resize" : "h-1 cursor-row-resize"
        }`}
        onMouseDown={handleDragStart}
      />
      <div className="overflow-hidden min-w-0 min-h-0" style={{ flex: 1 - node.ratio, transition }}>
        <SplitPane
          node={node.second}
          tabId={tabId}
          isTabActive={isTabActive}
          activePaneId={activePaneId}
          showFocusRing={showFocusRing}
          path={[...path, 1]}
        />
      </div>
    </div>
  );
}
