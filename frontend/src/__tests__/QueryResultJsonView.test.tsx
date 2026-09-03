import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryResultJsonView } from "@/components/query/QueryResultJsonView";
import { resultRowsToJson } from "@/lib/resultJson";
import { QueryViewModeToggle } from "@/components/query/QueryViewModeToggle";
import { TableDataTab } from "@/components/query/TableDataTab";
import { CELL_DISPLAY_MAX_CHARS } from "@/lib/cellValue";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQL, OpenTable } from "../../wailsjs/go/query/Query";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value, language, readOnly }: { value: string; language?: string; readOnly?: boolean }) => (
    <pre data-testid="code-editor" data-language={language} data-readonly={String(!!readOnly)}>
      {value}
    </pre>
  ),
}));

describe("resultRowsToJson", () => {
  const columns = ["id", "name"];
  const rows = [
    { id: 1, name: "ada", secret: "hidden" },
    { id: 2, name: null },
  ];

  it("serialises rows keyed by the given columns, in column order", () => {
    expect(JSON.parse(resultRowsToJson(rows, columns))).toEqual([
      { id: 1, name: "ada" },
      { id: 2, name: null },
    ]);
    // A column the user hid is not smuggled into the JSON.
    expect(resultRowsToJson(rows, columns)).not.toContain("secret");
  });

  it("applies pending cell edits so both views agree", () => {
    const edits = new Map<string, unknown>([["0:name", "grace"]]);

    expect(JSON.parse(resultRowsToJson(rows, columns, edits))[0]).toEqual({ id: 1, name: "grace" });
  });

  it("keeps long values whole — the grid truncates for layout, JSON must not", () => {
    const big = "x".repeat(CELL_DISPLAY_MAX_CHARS * 2);

    const parsed = JSON.parse(resultRowsToJson([{ id: 1, name: big }], columns));

    expect(parsed[0].name).toHaveLength(CELL_DISPLAY_MAX_CHARS * 2);
    expect(parsed[0].name).not.toContain("…");
  });

  it("keeps every column of a pending new row, whose unfilled cells are undefined", () => {
    // JSON cannot represent undefined — without a fallback the column disappears from the
    // document entirely, so the JSON view would not show what the grid shows.
    expect(JSON.parse(resultRowsToJson([{}], columns))).toEqual([{ id: "", name: "" }]);
    // A persisted NULL is JSON-representable and must stay null, not become "".
    expect(JSON.parse(resultRowsToJson([{ id: null, name: null }], columns))).toEqual([{ id: null, name: null }]);
  });

  it("renders an empty result as an empty array", () => {
    expect(resultRowsToJson([], columns)).toBe("[]");
  });
});

describe("QueryResultJsonView", () => {
  it("renders read-only JSON", () => {
    render(<QueryResultJsonView rows={[{ id: 1 }]} columns={["id"]} />);

    const editor = screen.getByTestId("code-editor");
    expect(editor).toHaveAttribute("data-language", "json");
    expect(editor).toHaveAttribute("data-readonly", "true");
  });

  it("shows the query error instead of a JSON body", () => {
    render(<QueryResultJsonView rows={[{ id: 1 }]} columns={["id"]} error="Unknown column 'statuz'" />);

    expect(screen.getByText(/Unknown column/)).toBeInTheDocument();
    expect(screen.queryByTestId("code-editor")).not.toBeInTheDocument();
  });
});

describe("QueryViewModeToggle", () => {
  it("reports the mode the user picks", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<QueryViewModeToggle value="table" onChange={onChange} />);

    expect(screen.getByRole("radio", { name: "query.tableView" })).toHaveAttribute("aria-checked", "true");
    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));

    expect(onChange).toHaveBeenCalledWith("json");
  });
});

function setupStores() {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "db",
          assetIcon: "",
          assetType: "database",
          driver: "mysql",
        },
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
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "users" }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
}

describe("TableDataTab JSON view", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(OpenTable).mockReset();
    setupStores();
  });

  it("switches between grid and JSON without re-querying, and carries pending edits", async () => {
    const user = userEvent.setup();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: {},
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 1,
        firstPage: [{ id: 1, name: "ada" }],
        pageSize: 1000,
      })
    );
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());

    // Edit a cell, then switch to JSON.
    const cell = document.querySelector('[data-cell-key="0:name"]') as HTMLElement;
    fireEvent.doubleClick(cell);
    const input = cell.querySelector("input") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "grace" } });
    fireEvent.blur(input);

    const callsBefore = vi.mocked(ExecuteSQL).mock.calls.length;
    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));

    const json = JSON.parse(screen.getByTestId("code-editor").textContent!);
    expect(json).toEqual([{ id: 1, name: "grace" }]);
    // The grid is gone, and switching issued no query of its own.
    expect(document.querySelector('[data-cell-key="0:name"]')).toBeNull();
    expect(vi.mocked(ExecuteSQL).mock.calls.length).toBe(callsBefore);

    // Switching back keeps the pending edit.
    await user.click(screen.getByRole("radio", { name: "query.tableView" }));
    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());
    expect(screen.getByTitle("query.applyChanges")).toBeEnabled();
  });
});
