import { describe, expect, it, beforeEach, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SqlEditorTab } from "@/components/query/SqlEditorTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { formatModKey } from "@/stores/shortcutStore";
import { ExecuteSQLPaged } from "../../../../wailsjs/go/query/Query";
import { mounted } from "./fakeSqlEditor";
import { BUILTIN_DRIVERS, provideDrivers } from "@/__tests__/dbDriverFixtures";

vi.mock("@/components/CodeEditor", async () => ({
  CodeEditor: (await import("./FakeCodeEditor")).FakeCodeEditor,
}));

const TWO = "SELECT 1;\nSELECT 2;";

function setup(sql: string) {
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
        innerTabs: [{ id: "sql-1", type: "sql", title: "SQL", sql, selectedDb: "appdb" }],
        activeInnerTabId: "sql-1",
        error: null,
      },
    },
  });
  render(<SqlEditorTab tabId="query-1" innerTabId="sql-1" />);
  return mounted.current!;
}

const executed = () => vi.mocked(ExecuteSQLPaged).mock.calls.map((c) => c[1]);
const history = () => {
  const tab = useQueryStore.getState().dbStates["query-1"].innerTabs[0];
  return tab.type === "sql" ? tab.history : undefined;
};

describe("SqlEditorTab run current statement", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQLPaged).mockReset();
    provideDrivers(BUILTIN_DRIVERS);
    vi.mocked(ExecuteSQLPaged).mockResolvedValue(
      JSON.stringify({ columns: ["id"], rows: [{ id: 1 }], total_count: 1 })
    );
  });

  it("runs the selection on mod+enter", async () => {
    const ed = setup(TWO);
    act(() => ed.select(0, 6));
    act(() => ed.runMod());
    await waitFor(() => expect(executed()).toEqual(["SELECT"]));
  });

  it("runs the statement at the cursor on mod+enter", async () => {
    const ed = setup(TWO);
    act(() => ed.setCursor(14));
    act(() => ed.runMod());
    await waitFor(() => expect(executed()).toEqual(["SELECT 2"]));
  });

  it("runs the whole editor on mod+shift+enter, ignoring the selection", async () => {
    const ed = setup(TWO);
    act(() => ed.select(0, 3));
    act(() => ed.runModShift());
    await waitFor(() => expect(executed()).toEqual([TWO]));
  });

  it("records the executed text in history and checks danger on it", async () => {
    const ed = setup("SELECT 1;\nDROP TABLE x;");
    act(() => ed.setCursor(2));
    act(() => ed.runMod());
    await waitFor(() => expect(executed()).toEqual(["SELECT 1"]));
    expect(history()).toEqual(["SELECT 1"]);
    expect(screen.queryByText("query.dangerousSqlTitle")).toBeNull();

    act(() => ed.setCursor(14));
    act(() => ed.runMod());
    expect(await screen.findByText("query.dangerousSqlTitle")).toBeInTheDocument();
    expect(executed()).toEqual(["SELECT 1"]);
    // moving the cursor while the dialog is open must not change what gets confirmed
    act(() => ed.setCursor(0));
    await userEvent.setup().click(screen.getAllByText("query.execute").at(-1)!);
    await waitFor(() => expect(executed()).toEqual(["SELECT 1", "DROP TABLE x"]));
    expect(history()).toEqual(["DROP TABLE x", "SELECT 1"]);
  });

  it("main button runs the current statement; dropdown offers current and all with shortcut hints", async () => {
    const ed = setup(TWO);
    const user = userEvent.setup();
    act(() => ed.setCursor(14));
    await user.click(screen.getByTestId("sql-execute-button"));
    await waitFor(() => expect(executed()).toEqual(["SELECT 2"]));
    expect(screen.getByTestId("sql-execute-button")).toHaveTextContent(formatModKey("Enter"));

    await user.click(screen.getByTestId("sql-execute-menu-button"));
    const current = await screen.findByRole("menuitem", { name: /query\.executeCurrent/ });
    const all = screen.getByRole("menuitem", { name: /query\.executeAll/ });
    expect(current).toHaveTextContent(formatModKey("Enter"));
    expect(all).toHaveTextContent(formatModKey("Enter", { shift: true }));
    await user.click(all);
    await waitFor(() => expect(executed()).toEqual(["SELECT 2", TWO]));
  });

  it("disables the run button when there is no statement at the cursor", async () => {
    const ed = setup("-- nothing here\n");
    expect(screen.getByTestId("sql-execute-button")).toBeDisabled();
    act(() => ed.runMod());
    expect(executed()).toEqual([]);

    act(() => ed.select(0, 7));
    expect(screen.getByTestId("sql-execute-button")).toBeEnabled();
  });

  it("hands Monaco only class names it preserves (it rewrites characters like '/' to spaces)", () => {
    const ed = setup(TWO);
    act(() => ed.setFocus(true));
    const options = ed.decorations()[0].options;
    const names = Object.entries(options)
      .filter(([key]) => /classname$/i.test(key))
      .map(([, value]) => value as string);
    expect(names.length).toBeGreaterThan(0);
    for (const name of names) {
      expect(name).toMatch(/^[A-Za-z0-9_\- ]+$/);
    }
  });

  it("highlights the current statement lines only while focused without a selection", () => {
    const ed = setup(TWO);
    act(() => ed.setCursor(14));
    expect(ed.decorations()).toEqual([]);

    act(() => ed.setFocus(true));
    expect(ed.decorations()).toHaveLength(1);
    expect(ed.decorations()[0].range).toMatchObject({ startLineNumber: 2, endLineNumber: 2 });
    expect(ed.decorations()[0].options).toMatchObject({ isWholeLine: true });

    act(() => ed.select(10, 14));
    expect(ed.decorations()).toEqual([]);

    act(() => ed.setCursor(1));
    expect(ed.decorations()[0].range).toMatchObject({ startLineNumber: 1, endLineNumber: 1 });

    act(() => ed.setFocus(false));
    expect(ed.decorations()).toEqual([]);
  });
});
