import { useLayoutStore } from "@/stores/layoutStore";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { useTerminalStore } from "@/stores/terminalStore";
import type { ShortcutAction } from "@/stores/shortcutStore";

export interface AppShellActionHandlers {
  onToggleAIPanel: () => void;
  onToggleSidebar: () => void;
  onToggleCommandPalette: () => void;
}

/**
 * Execute a workspace/app-level shortcut action.
 * Returns true if the action was recognized and dispatched.
 */
export function dispatchAppAction(action: ShortcutAction, handlers?: AppShellActionHandlers): boolean {
  if (action === "command.quickopen") {
    handlers?.onToggleCommandPalette();
    return true;
  }

  const tabStore = useTabStore.getState();
  const { tabs, activeTabId } = tabStore;

  const tabMatch = action.match(/^tab\.(\d)$/);
  if (tabMatch) {
    const idx = parseInt(tabMatch[1], 10) - 1;
    if (idx < tabs.length) {
      tabStore.activateTab(tabs[idx].id);
    }
    return true;
  }

  switch (action) {
    case "tab.close": {
      if (activeTabId) {
        const activeTab = tabs.find((t) => t.id === activeTabId);
        if (activeTab?.type === "terminal") {
          const termStore = useTerminalStore.getState();
          const data = termStore.tabData[activeTabId];
          if (data) {
            termStore.closePane(activeTabId, data.activePaneId);
          }
        } else {
          // 关系型数据库标签(有 dbStates)先关当前内层标签,内层全关完才关外层;关闭都受未提交修改拦截。
          const query = useQueryStore.getState();
          const innerId = query.dbStates[activeTabId]?.activeInnerTabId;
          if (innerId) query.closeInnerTab(activeTabId, innerId);
          else tabStore.closeTab(activeTabId);
        }
      }
      return true;
    }
    case "tab.prev": {
      if (tabs.length === 0) return true;
      const curIdx = tabs.findIndex((t) => t.id === activeTabId);
      const prevIdx = curIdx <= 0 ? tabs.length - 1 : curIdx - 1;
      tabStore.activateTab(tabs[prevIdx].id);
      return true;
    }
    case "tab.next": {
      if (tabs.length === 0) return true;
      const curIdx = tabs.findIndex((t) => t.id === activeTabId);
      const nextIdx = curIdx >= tabs.length - 1 ? 0 : curIdx + 1;
      tabStore.activateTab(tabs[nextIdx].id);
      return true;
    }
    case "split.vertical": {
      const activeTab = tabs.find((t) => t.id === activeTabId);
      if (activeTab?.type === "terminal" && activeTabId) {
        useTerminalStore.getState().splitPane(activeTabId, "vertical");
      }
      return true;
    }
    case "split.horizontal": {
      const activeTab = tabs.find((t) => t.id === activeTabId);
      if (activeTab?.type === "terminal" && activeTabId) {
        useTerminalStore.getState().splitPane(activeTabId, "horizontal");
      }
      return true;
    }
    case "panel.switch": {
      const st = useLayoutStore.getState();
      st.switchPanel();
      if (!st.leftPanelVisible) st.toggleVisible();
      return true;
    }
    case "panel.ai":
      handlers?.onToggleAIPanel();
      return true;
    case "panel.sidebar": {
      const layout = useLayoutStore.getState().tabBarLayout;
      if (layout === "left") {
        useLayoutStore.getState().toggleVisible();
      } else {
        handlers?.onToggleSidebar();
      }
      return true;
    }
    case "panel.toolbar":
      useLayoutStore.getState().toggleToolbar();
      return true;
    case "page.home": {
      const homeTab = tabs.find((t) => t.type === "terminal" || t.type === "info");
      if (homeTab) tabStore.activateTab(homeTab.id);
      return true;
    }
    case "page.settings": {
      const existing = tabs.find((t) => t.id === "settings");
      if (existing) {
        tabStore.activateTab("settings");
      } else {
        tabStore.openTab({
          id: "settings",
          type: "page",
          label: "settings",
          meta: { type: "page", pageId: "settings" },
        });
      }
      return true;
    }
    case "page.sshkeys": {
      const existing = tabs.find((t) => t.id === "sshkeys");
      if (existing) {
        tabStore.activateTab("sshkeys");
      } else {
        tabStore.openTab({
          id: "sshkeys",
          type: "page",
          label: "sshkeys",
          meta: { type: "page", pageId: "sshkeys" },
        });
      }
      return true;
    }
    default:
      return false;
  }
}
