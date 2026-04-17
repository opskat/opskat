import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Search, Server, Folder, MessageSquare, Settings, KeyRound, ScrollText, ArrowRightLeft } from "lucide-react";
import { cn } from "@opskat/ui";
import { useTabStore, type Tab, type PageTabMeta, type InfoTabMeta } from "@/stores/tabStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { getIconComponent, getIconColor } from "@/components/asset/IconPicker";
import { filterMatches, highlightMatch } from "@/lib/highlightMatch";
import { useLayoutStore, isCollapsed } from "@/stores/layoutStore";
import { SideTabItem, SideTabDragContext } from "./SideTabItem";
import { TabFilterInput } from "./TabFilterInput";
import { TabPanelMenu } from "./TabPanelMenu";

const pageTabMeta: Record<string, { icon: typeof Settings; labelKey: string }> = {
  settings: { icon: Settings, labelKey: "nav.settings" },
  forward: { icon: ArrowRightLeft, labelKey: "nav.forward" },
  sshkeys: { icon: KeyRound, labelKey: "nav.sshKeys" },
  audit: { icon: ScrollText, labelKey: "nav.audit" },
};

export function SideTabList() {
  const { t } = useTranslation();
  const tabs = useTabStore((s) => s.tabs);
  const activeTabId = useTabStore((s) => s.activeTabId);
  const activateTab = useTabStore((s) => s.activateTab);
  const closeTab = useTabStore((s) => s.closeTab);
  const reorderTab = useTabStore((s) => s.reorderTab);
  const moveTabTo = useTabStore((s) => s.moveTabTo);
  const tabData = useTerminalStore((s) => s.tabData);

  const width = useLayoutStore((s) => s.leftPanelWidth);
  const collapsed = isCollapsed({ leftPanelWidth: width });

  const filterOpen = useLayoutStore((s) => s.filterOpen);
  const setFilterOpen = useLayoutStore((s) => s.setFilterOpen);
  const requestOpenFilter = useLayoutStore((s) => s.requestOpenFilter);
  const [query, setQuery] = useState("");
  const dragKeyRef = useRef<string | null>(null);

  const resolveLabel = (tab: Tab): string => {
    if (tab.type === "page") {
      const meta = tab.meta as PageTabMeta;
      const pm = pageTabMeta[meta.pageId];
      if (pm) return t(pm.labelKey);
    }
    return tab.label;
  };

  const matchedWithLabel = useMemo(
    () =>
      tabs
        .map((tab) => ({ tab, displayLabel: resolveLabel(tab) }))
        .filter(({ displayLabel }) => filterMatches(displayLabel, query)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [tabs, query, t]
  );

  const resolveMeta = (
    tab: Tab
  ): {
    Icon: React.ComponentType<{ className?: string; style?: React.CSSProperties }>;
    iconStyle?: React.CSSProperties;
    indicatorColor?: string;
    extra?: React.ReactNode;
  } => {
    if (tab.type === "terminal") {
      const data = tabData[tab.id];
      const paneValues = data ? Object.values(data.panes) : [];
      const allDisconnected = paneValues.length > 0 && paneValues.every((p) => !p.connected);
      const Icon = tab.icon ? getIconComponent(tab.icon) : Server;
      const color = tab.icon ? getIconColor(tab.icon) : undefined;
      return {
        Icon,
        iconStyle: color ? { color } : undefined,
        indicatorColor: color,
        extra: allDisconnected ? <span className="h-1.5 w-1.5 rounded-full bg-destructive shrink-0" /> : undefined,
      };
    }
    if (tab.type === "ai") {
      return { Icon: MessageSquare };
    }
    if (tab.type === "query" || tab.type === "info") {
      const Icon = tab.icon
        ? getIconComponent(tab.icon)
        : tab.type === "info" && (tab.meta as InfoTabMeta).targetType === "group"
          ? Folder
          : Server;
      const color = tab.icon ? getIconColor(tab.icon) : undefined;
      return { Icon, iconStyle: color ? { color } : undefined, indicatorColor: color };
    }
    // page
    const meta = tab.meta as PageTabMeta;
    const pm = pageTabMeta[meta.pageId];
    if (pm) return { Icon: pm.icon };
    const Icon = tab.icon ? getIconComponent(tab.icon) : Server;
    const color = tab.icon ? getIconColor(tab.icon) : undefined;
    return { Icon, iconStyle: color ? { color } : undefined, indicatorColor: color };
  };

  return (
    <div data-tab-panel className="flex flex-col h-full bg-background">
      {!collapsed && (
        <div className="flex items-center gap-1 px-2 py-1.5 border-b shrink-0">
          <span className="text-xs font-medium uppercase text-muted-foreground tracking-wide flex-1">
            {t("sideTabs.title")}
          </span>
          <span className="text-xs text-muted-foreground">
            {query
              ? t("sideTabs.countFiltered", { filtered: matchedWithLabel.length, total: tabs.length })
              : t("sideTabs.count", { count: tabs.length })}
          </span>
          <button
            type="button"
            onClick={() => setFilterOpen(!filterOpen)}
            className={cn(
              "shrink-0 rounded-sm p-1",
              filterOpen ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted hover:text-foreground"
            )}
            aria-label={t("shortcuts.panel.filter")}
          >
            <Search className="h-3.5 w-3.5" />
          </button>
          <TabPanelMenu mode="side" onOpenFilter={requestOpenFilter} />
        </div>
      )}

      {!collapsed && filterOpen && (
        <TabFilterInput
          autoFocus
          value={query}
          onChange={setQuery}
          onClose={() => {
            setFilterOpen(false);
            setQuery("");
          }}
          onEnter={() => {
            if (matchedWithLabel[0]) activateTab(matchedWithLabel[0].tab.id);
          }}
        />
      )}

      <SideTabDragContext.Provider value={{ dragKeyRef, reorder: reorderTab, moveTo: moveTabTo, tabs }}>
        <div className="flex-1 overflow-y-auto py-1 px-1">
          {matchedWithLabel.length === 0 ? (
            <p className="px-3 py-2 text-xs text-muted-foreground text-center">
              {tabs.length === 0 ? t("sideTabs.noTabs") : t("sideTabs.emptyHint")}
            </p>
          ) : (
            matchedWithLabel.map(({ tab, displayLabel }) => {
              const segments = highlightMatch(displayLabel, query);
              const meta = resolveMeta(tab);
              return (
                <SideTabItem
                  key={tab.id}
                  tab={tab}
                  isActive={tab.id === activeTabId}
                  collapsed={collapsed}
                  labelSegments={segments}
                  icon={meta.Icon}
                  iconStyle={meta.iconStyle}
                  indicatorColor={meta.indicatorColor}
                  extra={meta.extra}
                  onActivate={() => activateTab(tab.id)}
                  onClose={() => closeTab(tab.id)}
                />
              );
            })
          )}
        </div>
      </SideTabDragContext.Provider>
    </div>
  );
}
