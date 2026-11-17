import { useTranslation } from "react-i18next";
import { Server, TerminalSquare, X, type LucideIcon } from "lucide-react";
import {
  cn,
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@opskat/ui";
import { OVERVIEW_TAB_ID, useK8sStore, type K8sInnerTab } from "@/stores/k8sStore";
import { kindIcon } from "./kindMeta";

function tabIcon(id: string): LucideIcon {
  if (id === OVERVIEW_TAB_ID) return Server;
  if (id.startsWith("term:")) return TerminalSquare;
  // list:<typeKey> | res:<typeKey>:<ns>:<name>
  const typeKey = id.slice(id.indexOf(":") + 1).split(":")[0];
  return kindIcon(typeKey);
}

/** Inner tabs of the cluster page; the overview tab is fixed first and can't be closed. */
export function K8sInnerTabBar({
  assetId,
  tabs,
  activeTabId,
}: {
  assetId: number;
  tabs: K8sInnerTab[];
  activeTabId: string;
}) {
  const { t } = useTranslation();
  const setActiveTab = useK8sStore((s) => s.setActiveTab);
  const closeTabs = useK8sStore((s) => s.closeTabs);

  return (
    <div className="flex shrink-0 items-center overflow-x-auto border-b border-border bg-muted/30">
      {tabs.map((tab, idx) => {
        const isOverview = tab.id === OVERVIEW_TAB_ID;
        const Icon = tabIcon(tab.id);
        const others = tabs.filter((other) => other.id !== tab.id && other.id !== OVERVIEW_TAB_ID).map((o) => o.id);
        const left = tabs
          .slice(0, idx)
          .filter((other) => other.id !== OVERVIEW_TAB_ID)
          .map((o) => o.id);
        const right = tabs.slice(idx + 1).map((o) => o.id);
        return (
          <ContextMenu key={tab.id}>
            <ContextMenuTrigger className="contents">
              <div
                className={cn(
                  "flex cursor-pointer select-none items-center gap-1.5 whitespace-nowrap border-r border-border px-3 py-1.5 text-xs transition-colors duration-150",
                  tab.id === activeTabId
                    ? "-mb-[1px] border-b-2 border-b-primary bg-background font-medium"
                    : "hover:bg-muted/50"
                )}
                onClick={() => setActiveTab(assetId, tab.id)}
              >
                <Icon className="h-3 w-3" />
                {isOverview ? t("asset.k8sClusterOverview") : tab.label}
                {!isOverview && (
                  <button
                    className="ml-1 rounded-sm p-0.5 text-muted-foreground hover:bg-muted-foreground/20"
                    aria-label={t("tab.close")}
                    onClick={(e) => {
                      e.stopPropagation();
                      closeTabs(assetId, [tab.id]);
                    }}
                  >
                    <X className="h-2.5 w-2.5" />
                  </button>
                )}
              </div>
            </ContextMenuTrigger>
            <ContextMenuContent>
              {!isOverview && (
                <ContextMenuItem onClick={() => closeTabs(assetId, [tab.id])}>{t("tab.close")}</ContextMenuItem>
              )}
              <ContextMenuItem onClick={() => closeTabs(assetId, others, tab.id)} disabled={others.length === 0}>
                {t("tab.closeOthers")}
              </ContextMenuItem>
              {!isOverview && (
                <>
                  <ContextMenuSeparator />
                  <ContextMenuItem onClick={() => closeTabs(assetId, left)} disabled={left.length === 0}>
                    {t("tab.closeLeft")}
                  </ContextMenuItem>
                  <ContextMenuItem onClick={() => closeTabs(assetId, right)} disabled={right.length === 0}>
                    {t("tab.closeRight")}
                  </ContextMenuItem>
                </>
              )}
            </ContextMenuContent>
          </ContextMenu>
        );
      })}
    </div>
  );
}
