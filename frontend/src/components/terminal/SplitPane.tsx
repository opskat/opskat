import { useRef, useCallback } from "react";
import { Loader2 } from "lucide-react";
import { Terminal } from "./Terminal";
import { ConnectionProgress } from "./ConnectionProgress";
import { SessionToolbar } from "./SessionToolbar";
import { useTerminalStore, type SplitNode } from "@/stores/terminalStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";

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
    return (
      <TerminalPaneView
        tabId={tabId}
        sessionId={node.sessionId}
        isTabActive={isTabActive}
        isFocused={activePaneId === node.sessionId}
        showFocusRing={showFocusRing}
      />
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
    return (
      <ConnectionProgress
        connectionId={node.connectionId}
        isTabActive={isTabActive}
        isPaneActive={activePaneId === node.connectionId}
      />
    );
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

interface TerminalPaneViewProps {
  tabId: string;
  sessionId: string;
  isTabActive: boolean;
  isFocused: boolean;
  showFocusRing: boolean;
}

// 单个终端窗格：顶部挂本窗格的工具条。固定态常驻；隐藏态默认收起、
// 鼠标移到窗格顶部时向下滑出并浮在终端内容之上（不挤压内容）。
function TerminalPaneView({ tabId, sessionId, isTabActive, isFocused, showFocusRing }: TerminalPaneViewProps) {
  const toolbarPinned = useTerminalThemeStore((s) => s.toolbarPinned);

  const terminal = (
    <div className="flex-1 min-h-0">
      <Terminal sessionId={sessionId} active={isTabActive} tabId={tabId} />
    </div>
  );

  return (
    <div
      className="h-full w-full relative flex flex-col"
      onMouseDown={() => {
        if (!isFocused) {
          useTerminalStore.getState().setActivePaneId(tabId, sessionId);
        }
      }}
    >
      {showFocusRing && isFocused && (
        <div className="absolute inset-0 ring-1 ring-primary/40 rounded-sm pointer-events-none z-30" />
      )}
      {toolbarPinned ? (
        <>
          <SessionToolbar tabId={tabId} sessionId={sessionId} />
          {terminal}
        </>
      ) : (
        <>
          {terminal}
          {/* 隐藏态：透明悬停触发条 + 向下滑出的浮层工具条（被外层 overflow-hidden 裁掉上移部分）。 */}
          <div className="absolute inset-x-0 top-0 z-20">
            <div className="peer h-2 w-full" />
            {/* 未悬停时的极简提示手柄，悬停后淡出 */}
            <div className="pointer-events-none absolute left-1/2 top-1 h-1 w-8 -translate-x-1/2 rounded-full bg-foreground/15 transition-opacity duration-150 peer-hover:opacity-0" />
            {/* 工具条绝对定位到窗格顶端(而非跟在触发条之后)，这样 -translate-y-full 能完全收起、
                translate-y-0 能贴顶铺开——否则会因触发条高度的偏移，收起时露出一条、展开时顶部留缝。 */}
            <div className="absolute inset-x-0 top-0 -translate-y-full shadow-md transition-transform duration-200 ease-out peer-hover:translate-y-0 hover:translate-y-0">
              <SessionToolbar tabId={tabId} sessionId={sessionId} />
            </div>
          </div>
        </>
      )}
    </div>
  );
}

const SPLIT_TRANSITION = "flex 150ms ease-out";

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
  const firstRef = useRef<HTMLDivElement>(null);
  const secondRef = useRef<HTMLDivElement>(null);
  const isVertical = node.direction === "vertical";

  // 拖动期间直接改两个窗格的 DOM(不经 store，避免每次 mousemove 重渲染整棵终端树)，
  // 松开时只提交一次 setSplitRatio。
  const handleDragStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const container = containerRef.current;
      const first = firstRef.current;
      const second = secondRef.current;
      if (!container || !first || !second) return;

      const rect = container.getBoundingClientRect();
      let lastRatio: number | null = null;
      first.style.transition = "none";
      second.style.transition = "none";

      const onMouseMove = (e: MouseEvent) => {
        const raw = isVertical ? (e.clientX - rect.left) / rect.width : (e.clientY - rect.top) / rect.height;
        lastRatio = Math.max(0.1, Math.min(0.9, raw));
        first.style.flex = String(lastRatio);
        second.style.flex = String(1 - lastRatio);
      };

      const onMouseUp = () => {
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        first.style.transition = SPLIT_TRANSITION;
        second.style.transition = SPLIT_TRANSITION;
        if (lastRatio !== null) useTerminalStore.getState().setSplitRatio(tabId, path, lastRatio);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
      document.body.style.cursor = isVertical ? "col-resize" : "row-resize";
      document.body.style.userSelect = "none";
    },
    [tabId, path, isVertical]
  );

  return (
    <div ref={containerRef} className={`flex h-full w-full ${isVertical ? "flex-row" : "flex-col"}`}>
      <div
        ref={firstRef}
        className="overflow-hidden min-w-0 min-h-0"
        style={{ flex: node.ratio, transition: SPLIT_TRANSITION }}
      >
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
      <div
        ref={secondRef}
        className="overflow-hidden min-w-0 min-h-0"
        style={{ flex: 1 - node.ratio, transition: SPLIT_TRANSITION }}
      >
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
