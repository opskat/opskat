import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Activity, Key } from "lucide-react";
import { useResizeHandle } from "@opskat/ui";
import { RedisKeyBrowser } from "./RedisKeyBrowser";
import { RedisKeyDetail } from "./RedisKeyDetail";
import { RedisOpsPanel } from "./RedisOpsPanel";
import { useQueryStore } from "@/stores/queryStore";

interface RedisPanelProps {
  tabId: string;
}

export function RedisPanel({ tabId }: RedisPanelProps) {
  const { t } = useTranslation();
  const selectedKey = useQueryStore((s) => s.redisStates[tabId]?.selectedKey);
  const [activeView, setActiveView] = useState<"overview" | "key">("overview");
  const sidebarRef = useRef<HTMLDivElement>(null);
  const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
    defaultSize: 220,
    minSize: 160,
    maxSize: 400,
    targetRef: sidebarRef,
  });

  useEffect(() => {
    if (selectedKey) {
      setActiveView("key");
    } else {
      setActiveView("overview");
    }
  }, [selectedKey]);

  return (
    <div className="flex h-full w-full">
      {/* Left: Key browser */}
      <div ref={sidebarRef} className="shrink-0 border-r" style={{ width: sidebarWidth }}>
        <RedisKeyBrowser tabId={tabId} />
      </div>

      {/* Resize handle */}
      <div className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent" onMouseDown={handleMouseDown} />

      {/* Right: Redis pages */}
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex shrink-0 items-center overflow-x-auto border-b bg-muted/30">
          <button
            role="tab"
            aria-selected={activeView === "overview"}
            className={`flex items-center gap-1.5 border-r px-3 py-1.5 text-xs transition-colors ${
              activeView === "overview"
                ? "bg-background text-foreground"
                : "text-muted-foreground hover:bg-background/50 hover:text-foreground"
            }`}
            onClick={() => setActiveView("overview")}
          >
            <Activity className="size-3" />
            {t("query.redisOverview")}
          </button>
          {selectedKey && (
            <button
              role="tab"
              aria-selected={activeView === "key"}
              className={`flex max-w-[280px] items-center gap-1.5 border-r px-3 py-1.5 text-xs transition-colors ${
                activeView === "key"
                  ? "bg-background text-foreground"
                  : "text-muted-foreground hover:bg-background/50 hover:text-foreground"
              }`}
              onClick={() => setActiveView("key")}
            >
              <Key className="size-3 shrink-0" />
              <span className="truncate font-mono">{selectedKey}</span>
            </button>
          )}
        </div>

        <div className="relative min-h-0 flex-1">
          <div className="absolute inset-0" style={{ display: activeView === "overview" ? "block" : "none" }}>
            <RedisOpsPanel tabId={tabId} />
          </div>
          {selectedKey && (
            <div className="absolute inset-0" style={{ display: activeView === "key" ? "block" : "none" }}>
              <RedisKeyDetail tabId={tabId} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
