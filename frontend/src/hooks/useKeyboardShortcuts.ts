import { useEffect } from "react";
import { useTerminalStore } from "@/stores/terminalStore";
import { useShortcutStore, matchShortcut } from "@/stores/shortcutStore";
interface ShortcutHandlers {
  onToggleAIPanel: () => void;
  onToggleSidebar: () => void;
}

export function useKeyboardShortcuts({ onToggleAIPanel, onToggleSidebar }: ShortcutHandlers) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const { shortcuts, isRecording } = useShortcutStore.getState();
      if (isRecording) return;

      // Don't trigger in form fields, but allow in xterm terminal
      const target = e.target as HTMLElement;
      if (
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable) &&
        !target.closest(".xterm")
      ) {
        return;
      }

      const action = matchShortcut(e, shortcuts);
      if (!action) return;

      e.preventDefault();
      e.stopPropagation();

      const { tabs, activeTabId, assetInfoOpen, setActiveTab, openAssetInfo, closeAssetInfo, splitPane, closePane } =
        useTerminalStore.getState();

      // Build a virtual tab list: [asset info (if open), ...terminal tabs]
      // Asset info tab is represented as null, terminal tabs by their id
      const allTabIds: (string | null)[] = [];
      if (assetInfoOpen) allTabIds.push(null);
      for (const tab of tabs) allTabIds.push(tab.id);

      // Current active: null means asset info is showing
      const currentId = activeTabId ?? (assetInfoOpen ? null : undefined);

      const switchTo = (id: string | null) => {
        if (id === null) {
          openAssetInfo();
        } else {
          setActiveTab(id);
        }
      };

      // Tab switching: tab.1 ~ tab.9
      const tabMatch = action.match(/^tab\.(\d)$/);
      if (tabMatch) {
        const idx = parseInt(tabMatch[1]) - 1;
        if (idx < allTabIds.length) {
          switchTo(allTabIds[idx]);
        }
        return;
      }

      switch (action) {
        case "tab.close": {
          // Close asset info tab if it's currently active
          if (currentId === null && assetInfoOpen) {
            closeAssetInfo();
            break;
          }
          if (!activeTabId) break;
          const tab = tabs.find((t) => t.id === activeTabId);
          if (tab) {
            closePane(activeTabId, tab.activePaneId);
          }
          break;
        }
        case "tab.prev": {
          if (allTabIds.length === 0) break;
          const curIdx = currentId === undefined ? -1 : allTabIds.indexOf(currentId);
          const prevIdx = curIdx <= 0 ? allTabIds.length - 1 : curIdx - 1;
          switchTo(allTabIds[prevIdx]);
          break;
        }
        case "tab.next": {
          if (allTabIds.length === 0) break;
          const curIdx = currentId === undefined ? -1 : allTabIds.indexOf(currentId);
          const nextIdx = curIdx >= allTabIds.length - 1 ? 0 : curIdx + 1;
          switchTo(allTabIds[nextIdx]);
          break;
        }
        case "split.vertical": {
          if (!activeTabId) break;
          splitPane(activeTabId, "vertical");
          break;
        }
        case "split.horizontal": {
          if (!activeTabId) break;
          splitPane(activeTabId, "horizontal");
          break;
        }
        case "panel.ai":
          onToggleAIPanel();
          break;
        case "panel.sidebar":
          onToggleSidebar();
          break;
      }
    };

    window.addEventListener("keydown", handler, true);
    return () => window.removeEventListener("keydown", handler, true);
  }, [onToggleAIPanel, onToggleSidebar]);
}
