import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ExecuteSQL, OpenTable } from "../../wailsjs/go/query/Query";

// Pasting a copied row into an editable table is the flow the report names: copy a row in
// one table, add a blank row here, paste. These tests drive the real tab, so the row-index
// arithmetic between the grid's coordinate space and the pending-edit staging is covered.

const readText = vi.fn();

function openTablePayload(rows: Record<string, unknown>[]) {
  return JSON.stringify({
    columns: ["id", "name"],
    columnTypes: {},
    columnRules: [],
    primaryKeys: ["id"],
    totalCount: rows.length,
    firstPage: rows,
    pageSize: 1000,
  });
}

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

const ROWS = [
  { id: 1, name: "ada" },
  { id: 2, name: "bob" },
];

const gridContainer = () => document.querySelector(".query-table-scroll") as HTMLElement;
const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`) as HTMLElement;
const pasteKey = { key: "v", code: "KeyV", ctrlKey: true, metaKey: true };

const executeSqlCalls = () => vi.mocked(ExecuteSQL).mock.calls.map(([, sql]) => String(sql));
const insertCalls = () => executeSqlCalls().filter((sql) => sql.startsWith("INSERT"));

async function renderLoaded() {
  vi.mocked(OpenTable).mockResolvedValue(openTablePayload(ROWS));
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
  await waitFor(() => expect(cell("0:id")).toBeTruthy());
}

describe("TableDataTab keyboard paste", () => {
  beforeEach(() => {
    readText.mockReset();
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(OpenTable).mockReset();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn(), readText },
    });
    setupStores();
  });

  it("fills a blank row added for the paste and inserts the pasted values", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    readText.mockResolvedValue("9\tzoe");
    await renderLoaded();

    fireEvent.click(screen.getByTitle("query.addRow"));
    // Adding a row opens the first cell's editor; the grid only pastes with the editor closed.
    fireEvent.keyDown(document.querySelector('[data-cell-key="2:id"] input') as HTMLInputElement, { key: "Escape" });

    fireEvent.keyDown(gridContainer(), pasteKey);

    await waitFor(() => expect(cell("2:id")).toHaveTextContent("9"));
    expect(cell("2:name")).toHaveTextContent("zoe");

    fireEvent.click(screen.getByTitle("query.applyChanges"));
    fireEvent.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => expect(insertCalls()).toHaveLength(1));
    expect(insertCalls()[0]).toContain("(`id`, `name`) VALUES ('9', 'zoe')");
  });

  it("anchors a newly added row at the first visible column", async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.click(screen.getByTitle("query.displaySettings"));
    await user.click(screen.getByRole("menuitemcheckbox", { name: "id" }));
    await user.keyboard("{Escape}");
    await user.click(screen.getByTitle("query.addRow"));

    expect(document.querySelector('[data-cell-key="2:name"] input')).toBeTruthy();
    fireEvent.keyDown(document.querySelector('[data-cell-key="2:name"] input') as HTMLInputElement, { key: "Escape" });
    readText.mockResolvedValue("zoe");
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn(), readText },
    });

    fireEvent.keyDown(gridContainer(), pasteKey);

    await waitFor(() => expect(cell("2:name")).toHaveTextContent("zoe"));
  });

  it("appends unsaved rows for a block past the last row and inserts them", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    readText.mockResolvedValue("8\terin\n9\tzoe");
    await renderLoaded();

    fireEvent.click(cell("1:id"));

    fireEvent.keyDown(gridContainer(), pasteKey);

    await waitFor(() => expect(cell("1:id")).toHaveTextContent("8"));
    expect(cell("2:name")).toHaveTextContent("zoe");

    fireEvent.click(screen.getByTitle("query.applyChanges"));
    fireEvent.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => expect(insertCalls()).toHaveLength(1));
    expect(insertCalls()[0]).toContain("(`id`, `name`) VALUES ('9', 'zoe')");
    // The first pasted row overwrites the existing row 2, so it stages an UPDATE.
    expect(executeSqlCalls().some((sql) => sql.startsWith("UPDATE") && sql.includes("'8'"))).toBe(true);
  });
});
