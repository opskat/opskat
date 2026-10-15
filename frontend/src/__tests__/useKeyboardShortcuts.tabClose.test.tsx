import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac } from "../stores/shortcutStore";
import { useTabStore, type Tab } from "../stores/tabStore";
import { useQueryStore, type DatabaseTabState, type InnerTab } from "../stores/queryStore";

function pressClose() {
  const binding = DEFAULT_SHORTCUTS["tab.close"];
  document.body.dispatchEvent(
    new KeyboardEvent("keydown", {
      code: binding.code,
      metaKey: isMac ? binding.mod : false,
      ctrlKey: isMac ? binding.ctrl : binding.mod,
      shiftKey: binding.shift,
      altKey: binding.alt,
      bubbles: true,
      cancelable: true,
    })
  );
}

function queryTab(id: string, assetType: "database" | "redis"): Tab {
  return {
    id,
    type: "query",
    label: id,
    meta: { type: "query", assetId: 1, assetName: id, assetIcon: "", assetType },
  };
}

function dbState(innerTabs: InnerTab[], activeInnerTabId: string | null): DatabaseTabState {
  return {
    databases: [],
    tables: {},
    loadingTables: {},
    expandedDbs: [],
    expandedSchemas: {},
    loadingDbs: false,
    innerTabs,
    activeInnerTabId,
    error: null,
  };
}

const tabIds = () => useTabStore.getState().tabs.map((t) => t.id);
const innerIds = (tabId: string) => useQueryStore.getState().dbStates[tabId]?.innerTabs.map((t) => t.id);

describe("useKeyboardShortcuts — tab.close in a database tab", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    useShortcutStore.setState({ shortcuts: { ...DEFAULT_SHORTCUTS }, isRecording: false });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
  });

  it("closes the active inner tab first and keeps the database tab open", () => {
    useTabStore.setState({ tabs: [queryTab("query-1", "database")], activeTabId: "query-1", unsavedTabIds: [] });
    useQueryStore.setState({
      dbStates: {
        "query-1": dbState(
          [
            { id: "sql:1", type: "sql", title: "SQL 1" },
            { id: "sql:2", type: "sql", title: "SQL 2" },
          ],
          "sql:2"
        ),
      },
    });

    pressClose();

    expect(innerIds("query-1")).toEqual(["sql:1"]);
    expect(tabIds()).toEqual(["query-1"]);
  });

  it("closes the database tab once it has no inner tabs", () => {
    useTabStore.setState({ tabs: [queryTab("query-1", "database")], activeTabId: "query-1", unsavedTabIds: [] });
    useQueryStore.setState({ dbStates: { "query-1": dbState([], null) } });

    pressClose();

    expect(tabIds()).toEqual([]);
  });

  it("keeps an edited inner tab open and asks for a decision", () => {
    useTabStore.setState({ tabs: [queryTab("query-1", "database")], activeTabId: "query-1", unsavedTabIds: [] });
    useQueryStore.setState({
      dbStates: { "query-1": dbState([{ id: "table:a.b", type: "table", database: "a", table: "b" }], "table:a.b") },
    });
    useQueryStore.getState().setInnerTabDirty("query-1", "table:a.b", true);

    pressClose();

    expect(innerIds("query-1")).toEqual(["table:a.b"]);
    expect(useQueryStore.getState().dbStates["query-1"].closeRequest?.dirtyIds).toEqual(["table:a.b"]);
  });

  it("closes other query tabs (e.g. Redis) directly as before", () => {
    useTabStore.setState({
      tabs: [queryTab("query-1", "database"), queryTab("query-2", "redis")],
      activeTabId: "query-2",
      unsavedTabIds: [],
    });
    useQueryStore.setState({
      dbStates: { "query-1": dbState([{ id: "sql:1", type: "sql", title: "SQL 1" }], "sql:1") },
    });

    pressClose();

    expect(tabIds()).toEqual(["query-1"]);
    expect(innerIds("query-1")).toEqual(["sql:1"]);
  });
});
