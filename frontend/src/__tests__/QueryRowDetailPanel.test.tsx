import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryRowDetailPanel } from "@/components/query/QueryRowDetailPanel";
import { TableDataTab } from "@/components/query/TableDataTab";
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

describe("QueryRowDetailPanel", () => {
  const columns = ["id", "name"];
  const rows = [
    { id: 1, name: "ada" },
    { id: 2, name: "bob" },
  ];

  it("shows the current row as read-only JSON", () => {
    render(<QueryRowDetailPanel rows={rows} columns={columns} rowIdx={1} onClose={vi.fn()} />);

    const editor = screen.getByTestId("code-editor");
    expect(JSON.parse(editor.textContent!)).toEqual({ id: 2, name: "bob" });
    expect(editor).toHaveAttribute("data-readonly", "true");
    expect(editor).toHaveAttribute("data-language", "json");
  });

  it("applies pending edits so the panel agrees with the grid", () => {
    const edits = new Map<string, unknown>([["1:name", "grace"]]);
    render(<QueryRowDetailPanel rows={rows} columns={columns} rowIdx={1} edits={edits} onClose={vi.fn()} />);

    expect(JSON.parse(screen.getByTestId("code-editor").textContent!)).toEqual({ id: 2, name: "grace" });
  });

  it("shows an empty state with no current row", () => {
    render(<QueryRowDetailPanel rows={rows} columns={columns} rowIdx={null} onClose={vi.fn()} />);

    expect(screen.queryByTestId("code-editor")).not.toBeInTheDocument();
    expect(screen.getByText("query.rowDetailEmpty")).toBeInTheDocument();
  });

  it("closes through the header action", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<QueryRowDetailPanel rows={rows} columns={columns} rowIdx={0} onClose={onClose} />);

    await user.click(screen.getByTitle("action.close"));

    expect(onClose).toHaveBeenCalledOnce();
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

describe("TableDataTab row detail panel", () => {
  beforeEach(() => {
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(OpenTable).mockReset();
    setupStores();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: {},
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 2,
        firstPage: [
          { id: 1, name: "ada" },
          { id: 2, name: "bob" },
        ],
        pageSize: 1000,
      })
    );
  });

  it("stays closed until the toolbar action opens it, then follows the current row", async () => {
    const user = userEvent.setup();
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());

    expect(screen.queryByText("query.rowDetail")).not.toBeInTheDocument();

    await user.click(screen.getByTitle("query.rowDetail"));
    // Nothing selected yet — the panel is open but empty.
    expect(screen.getByText("query.rowDetailEmpty")).toBeInTheDocument();

    fireEvent.click(document.querySelector('[data-cell-key="1:name"]') as HTMLElement);

    await waitFor(() =>
      expect(JSON.parse(screen.getByTestId("code-editor").textContent!)).toEqual({ id: 2, name: "bob" })
    );
  });

  it("follows a row picked from the row number gutter", async () => {
    const user = userEvent.setup();
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
    await waitFor(() => expect(document.querySelector('[data-row-header-key="0"]')).toBeTruthy());
    await user.click(screen.getByTitle("query.rowDetail"));

    fireEvent.click(document.querySelector('[data-row-header-key="0"]') as HTMLElement);

    await waitFor(() =>
      expect(JSON.parse(screen.getByTestId("code-editor").textContent!)).toEqual({ id: 1, name: "ada" })
    );
  });

  it("empties but stays open when the row set is refreshed", async () => {
    const user = userEvent.setup();
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
    await waitFor(() => expect(document.querySelector('[data-cell-key="0:name"]')).toBeTruthy());
    await user.click(screen.getByTitle("query.rowDetail"));
    fireEvent.click(document.querySelector('[data-cell-key="0:name"]') as HTMLElement);
    await waitFor(() => expect(screen.getByTestId("code-editor")).toBeTruthy());

    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ columns: ["id", "name"], rows: [{ id: 9, name: "cy" }] }));
    await user.click(screen.getByTitle(/^query\.refreshTable/));

    await waitFor(() => expect(screen.getByText("query.rowDetailEmpty")).toBeInTheDocument());
  });
});
