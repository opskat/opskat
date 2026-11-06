import { render, renderHook, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, FIND_SCOPE_ATTR, isMac } from "../stores/shortcutStore";
import { useLayoutStore } from "../stores/layoutStore";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { SqlEditorTab } from "../components/query/SqlEditorTab";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

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

function focusable(parent: HTMLElement, tag = "div"): HTMLElement {
  const el = document.createElement(tag);
  if (tag === "div") el.tabIndex = 0;
  parent.appendChild(el);
  return el;
}

// 真实结构：Monaco 的焦点元素是 EditContext 的 div.native-edit-context，
// 既不是 input/textarea，也不是 contenteditable。
function monacoFocusTarget(parent: HTMLElement = document.body): HTMLElement {
  const editor = document.createElement("div");
  editor.className = "monaco-editor";
  parent.appendChild(editor);
  const ctx = focusable(editor);
  ctx.className = "native-edit-context";
  return ctx;
}

function renderSqlEditorTab() {
  provideDrivers(BUILTIN_DRIVERS);
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
  // FakeCodeEditor 渲染的是 <pre>，在里面放一个与 Monaco 焦点元素同构的节点。
  return monacoFocusTarget(screen.getByTestId("code-editor"));
}

describe("useKeyboardShortcuts — Cmd/Ctrl+F dispatch by focus", () => {
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

  it("leaves the key to a database result grid so it can open its find bar", () => {
    const region = document.createElement("div");
    region.setAttribute(FIND_SCOPE_ATTR, "");
    document.body.appendChild(region);

    const ev = press("panel.filter", focusable(region));

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("leaves Cmd/Ctrl+F to the SQL editor so Monaco's find widget opens instead of the tab filter", () => {
    const ev = press("panel.filter", renderSqlEditorTab());

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("leaves Cmd/Ctrl+F to any other Monaco editor too (e.g. Mongo / remote file editor)", () => {
    const ev = press("panel.filter", monacoFocusTarget());

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("still opens the tab filter when focus is anywhere else", () => {
    const ev = press("panel.filter", focusable(document.body));

    expect(requestOpenFilter).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("does not take the key from a focused text field", () => {
    const ev = press("panel.filter", focusable(document.body, "textarea"));

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("keeps handling other global shortcuts inside the SQL editor (Cmd/Ctrl+W)", () => {
    const ev = press("tab.close", renderSqlEditorTab());

    expect(ev.defaultPrevented).toBe(true);
  });
});
