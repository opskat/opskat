import { describe, it, expect, beforeEach } from "vitest";
import { applyStartupPreference, useTabStore } from "../stores/tabStore";
import type { Tab } from "../stores/tabStore";

function makeTerminalTab(id: string, assetId: number): Tab {
  return {
    id,
    type: "terminal",
    label: `Server ${assetId}`,
    meta: {
      type: "terminal",
      assetId,
      assetName: `Server ${assetId}`,
      assetIcon: "",
      host: "10.0.0.1",
      port: 22,
      username: "root",
    },
  };
}

function makeQueryTab(assetId: number): Tab {
  return {
    id: `query-${assetId}`,
    type: "query",
    label: `DB ${assetId}`,
    meta: {
      type: "query",
      assetId,
      assetName: `DB ${assetId}`,
      assetIcon: "",
      assetType: "database" as const,
      driver: "mysql",
    },
  };
}

describe("tabStore", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
  });

  describe("openTab", () => {
    it("should add a new tab and activate it", () => {
      const tab = makeTerminalTab("t1", 1);
      useTabStore.getState().openTab(tab);

      const state = useTabStore.getState();
      expect(state.tabs).toHaveLength(1);
      expect(state.tabs[0].id).toBe("t1");
      expect(state.activeTabId).toBe("t1");
    });

    it("should not duplicate tab with same id", () => {
      const tab = makeTerminalTab("t1", 1);
      useTabStore.getState().openTab(tab);
      useTabStore.getState().openTab(tab);

      expect(useTabStore.getState().tabs).toHaveLength(1);
    });

    it("should activate existing tab when opened again", () => {
      const tab1 = makeTerminalTab("t1", 1);
      const tab2 = makeTerminalTab("t2", 2);
      useTabStore.getState().openTab(tab1);
      useTabStore.getState().openTab(tab2);
      expect(useTabStore.getState().activeTabId).toBe("t2");

      useTabStore.getState().openTab(tab1);
      expect(useTabStore.getState().activeTabId).toBe("t1");
    });

    it("should not activate when activate=false", () => {
      const tab1 = makeTerminalTab("t1", 1);
      const tab2 = makeTerminalTab("t2", 2);
      useTabStore.getState().openTab(tab1);
      useTabStore.getState().openTab(tab2, false);

      expect(useTabStore.getState().activeTabId).toBe("t1");
      expect(useTabStore.getState().tabs).toHaveLength(2);
    });
  });

  describe("closeTab", () => {
    it("should remove the tab", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      useTabStore.getState().closeTab("t1");

      const state = useTabStore.getState();
      expect(state.tabs).toHaveLength(1);
      expect(state.tabs[0].id).toBe("t2");
    });

    it("should activate neighbor when active tab is closed", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      useTabStore.getState().openTab(makeTerminalTab("t3", 3));
      useTabStore.getState().activateTab("t2");

      useTabStore.getState().closeTab("t2");
      // Should activate t3 (next tab at same index)
      expect(useTabStore.getState().activeTabId).toBe("t3");
    });

    it("should activate last tab when closing the last position", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      useTabStore.getState().activateTab("t2");

      useTabStore.getState().closeTab("t2");
      expect(useTabStore.getState().activeTabId).toBe("t1");
    });

    it("should set activeTabId to null when last tab is closed", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().closeTab("t1");
      expect(useTabStore.getState().activeTabId).toBeNull();
    });
  });

  describe("replaceTabId", () => {
    it("should replace tab id and update activeTabId", () => {
      useTabStore.getState().openTab(makeTerminalTab("conn-1", 1));
      useTabStore.getState().replaceTabId("conn-1", "session-1");

      const state = useTabStore.getState();
      expect(state.tabs[0].id).toBe("session-1");
      expect(state.activeTabId).toBe("session-1");
    });

    it("should not change activeTabId if replaced tab is not active", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      // t2 is active
      useTabStore.getState().replaceTabId("t1", "t1-new");

      expect(useTabStore.getState().activeTabId).toBe("t2");
      expect(useTabStore.getState().tabs[0].id).toBe("t1-new");
    });
  });

  describe("reorderTab", () => {
    it("should swap tab positions", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      useTabStore.getState().openTab(makeTerminalTab("t3", 3));

      useTabStore.getState().reorderTab("t3", "t1");

      const ids = useTabStore.getState().tabs.map((t) => t.id);
      expect(ids).toEqual(["t3", "t1", "t2"]);
    });
  });

  describe("closeOtherTabs", () => {
    it("should keep only the specified tab", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeQueryTab(2));
      useTabStore.getState().openTab(makeTerminalTab("t3", 3));

      useTabStore.getState().closeOtherTabs("t1");

      const state = useTabStore.getState();
      expect(state.tabs).toHaveLength(1);
      expect(state.tabs[0].id).toBe("t1");
      expect(state.activeTabId).toBe("t1");
    });
  });

  describe("closeLeftTabs / closeRightTabs", () => {
    it("should close tabs to the left", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      useTabStore.getState().openTab(makeTerminalTab("t3", 3));

      useTabStore.getState().closeLeftTabs("t2");

      const ids = useTabStore.getState().tabs.map((t) => t.id);
      expect(ids).toEqual(["t2", "t3"]);
    });

    it("should close tabs to the right", () => {
      useTabStore.getState().openTab(makeTerminalTab("t1", 1));
      useTabStore.getState().openTab(makeTerminalTab("t2", 2));
      useTabStore.getState().openTab(makeTerminalTab("t3", 3));

      useTabStore.getState().closeRightTabs("t2");

      const ids = useTabStore.getState().tabs.map((t) => t.id);
      expect(ids).toEqual(["t1", "t2"]);
    });
  });

  describe("startup preference", () => {
    it("keeps restored tabs when startup_tab is not home", () => {
      const tab = makeTerminalTab("t1", 1);

      expect(applyStartupPreference({ tabs: [tab], activeTabId: "t1" })).toEqual({
        tabs: [tab],
        activeTabId: "t1",
      });
    });

    it("skips restored tabs when startup_tab is home", () => {
      localStorage.setItem("startup_tab", "home");

      expect(applyStartupPreference({ tabs: [makeTerminalTab("t1", 1)], activeTabId: "t1" })).toEqual({
        tabs: [],
        activeTabId: null,
      });
    });
  });
});
