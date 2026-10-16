import { render, renderHook, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac } from "../stores/shortcutStore";
import { useLayoutStore } from "../stores/layoutStore";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { SqlEditorTab } from "../components/query/SqlEditorTab";

vi.mock("@/components/CodeEditor", async () => ({
  CodeEditor: (await import("../components/query/__tests__/FakeCodeEditor")).FakeCodeEditor,
}));

function press(action: "panel.filter" | "tab.close", target: HTMLElement): KeyboardEvent {
  const binding = DEFAULT_SHORTCUTS[action];
  const ev = new KeyboardEvent("keydown", {
    code: binding.code,
    metaKey: isMac ? binding.mod : false,
    ctrlKey: isMac ? binding.ctrl : binding.mod,
    shiftKey: binding.shift,
    altKey: binding.alt,
    bubbles: true,
    cancelable: true,
  });
  target.focus();
  target.dispatchEvent(ev);
  return ev;
}

// 真实结构：Monaco 的焦点元素是 EditContext 的 div.native-edit-context，
// 既不是 input/textarea，也不是 contenteditable。
function monacoFocusTarget(parent: HTMLElement = document.body): HTMLElement {
  const editor = document.createElement("div");
  editor.className = "monaco-editor";
  const ctx = document.createElement("div");
  ctx.className = "native-edit-context";
  ctx.tabIndex = 0;
  editor.appendChild(ctx);
  parent.appendChild(editor);
  return ctx;
}

function renderSqlEditorTab() {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 1, assetName: "db", assetIcon: "", assetType: "database", driver: "mysql" },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["users"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "sql-1", type: "sql", title: "SQL", sql: "SELECT 1", selectedDb: "appdb" }],
        activeInnerTabId: "sql-1",
        error: null,
      },
    },
  });
  render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
  // FakeCodeEditor 渲染的是 <pre>，让它像 Monaco 的焦点元素一样可聚焦。
  const editor = screen.getByTestId("code-editor");
  return monacoFocusTarget(editor);
}

describe("useKeyboardShortcuts — Monaco editor focus", () => {
  let requestOpenFilter: ReturnType<typeof vi.fn<() => void>>;

  beforeEach(() => {
    document.body.innerHTML = "";
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      isRecording: false,
      disableShortcutsInTerminal: false,
    });
    requestOpenFilter = vi.fn<() => void>();
    useLayoutStore.setState({ requestOpenFilter });
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
  });

  it("leaves Cmd/Ctrl+F to the SQL editor so Monaco's find widget opens instead of the tab filter", () => {
    const ev = press("panel.filter", renderSqlEditorTab());

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("still opens the tab filter from a Monaco editor outside the SQL editor (e.g. Mongo / remote file editor)", () => {
    const ev = press("panel.filter", monacoFocusTarget());

    expect(requestOpenFilter).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("still opens the tab filter when focus is outside any editor", () => {
    const el = document.createElement("div");
    el.tabIndex = 0;
    document.body.appendChild(el);

    const ev = press("panel.filter", el);

    expect(requestOpenFilter).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("keeps handling other global shortcuts inside the SQL editor (Cmd/Ctrl+W)", () => {
    const ev = press("tab.close", renderSqlEditorTab());

    expect(ev.defaultPrevented).toBe(true);
  });
});
