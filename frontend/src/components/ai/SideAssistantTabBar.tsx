import { X } from "lucide-react";
import { cn, Button } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import type { SidebarAITab, SidebarTabStatus } from "@/stores/aiStore";

interface SideAssistantTabBarProps {
  tabs: SidebarAITab[];
  activeTabId: string | null;
  getStatus: (tabId: string) => SidebarTabStatus;
  onActivate: (tabId: string) => void;
  onClose: (tabId: string) => void;
}

const statusClassNames: Record<Exclude<SidebarTabStatus, null>, string> = {
  waiting_approval: "bg-amber-500",
  running: "bg-sky-500",
  done: "bg-emerald-500",
  error: "bg-rose-500",
};

export function SideAssistantTabBar({ tabs, activeTabId, getStatus, onActivate, onClose }: SideAssistantTabBarProps) {
  const { t } = useTranslation();

  return (
    <div
      className="flex h-full flex-col"
      role="tablist"
      aria-orientation="vertical"
      aria-label={t("ai.sidebar.sessions")}
    >
      <div className="flex items-center justify-between border-b border-panel-divider px-3 py-2">
        <span className="text-[10px] font-semibold uppercase tracking-[0.18em] text-muted-foreground/80">
          {t("ai.sidebar.sessions")}
        </span>
        <span className="rounded-full bg-background px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
          {tabs.length}
        </span>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        <div className="space-y-1.5">
          {tabs.map((tab) => {
            const isActive = tab.id === activeTabId;
            const status = getStatus(tab.id);
            const subtitle = status ? t(`ai.sidebar.status.${status}`) : t("ai.sidebar.newChat");
            return (
              <div
                key={tab.id}
                className={cn(
                  "group relative min-w-0 rounded-xl border text-xs transition-colors",
                  isActive
                    ? "border-primary/30 bg-background text-foreground shadow-sm ring-1 ring-primary/10"
                    : "border-transparent bg-background/35 text-muted-foreground hover:bg-background/80"
                )}
                role="presentation"
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={isActive}
                  className="flex w-full min-w-0 items-start gap-2 rounded-[inherit] px-2.5 py-2.5 pr-8 text-left"
                  onClick={() => onActivate(tab.id)}
                  title={tab.title || t("ai.newConversation")}
                >
                  {status ? (
                    <span
                      className={cn("mt-1 h-1.5 w-1.5 shrink-0 rounded-full", statusClassNames[status])}
                      title={t(`ai.sidebar.status.${status}`)}
                    />
                  ) : (
                    <span className="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-transparent" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-xs font-medium leading-5 text-foreground">
                      {tab.title || t("ai.newConversation")}
                    </span>
                    <span className="block truncate text-[11px] leading-4 text-muted-foreground">{subtitle}</span>
                  </span>
                </button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={cn(
                    "absolute right-1.5 top-1.5 h-5 w-5 shrink-0 rounded-md opacity-0 transition-opacity hover:opacity-100",
                    isActive && "opacity-70"
                  )}
                  onClick={(event) => {
                    event.stopPropagation();
                    onClose(tab.id);
                  }}
                  title={t("tab.close")}
                  aria-label={t("tab.close")}
                >
                  <X className="h-3 w-3" />
                </Button>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
