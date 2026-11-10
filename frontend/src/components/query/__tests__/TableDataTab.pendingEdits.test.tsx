import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { CountTableRows, ExecuteSQL, OpenTable, QueryTableRows } from "../../../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, mockGridEditStatements, provideDrivers } from "@/__tests__/dbDriverFixtures";

const ROWS = [
  { id: 1, name: "ada" },
  { id: 2, name: "bob" },
  { id: 3, name: "cy" },
];

function setupStores() {
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
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "users" }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
}

const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`) as HTMLElement;
const gutter = (idx: number) => document.querySelector(`[data-row-header-key="${idx}"]`) as HTMLElement;

function editCell(key: string, value: string) {
  fireEvent.doubleClick(cell(key));
  const input = cell(key).querySelector("input") as HTMLInputElement;
  fireEvent.change(input, { target: { value } });
  fireEvent.keyDown(input, { key: "Enter" });
}

async function renderLoaded() {
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
  await waitFor(() => expect(cell("0:name")).toBeTruthy());
  return userEvent.setup();
}

describe("TableDataTab pending edits", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    mockGridEditStatements();
    for (const fn of [OpenTable, QueryTableRows, CountTableRows, ExecuteSQL]) vi.mocked(fn).mockReset();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: {},
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 3,
        firstPage: ROWS,
        pageSize: 1000,
      })
    );
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    vi.mocked(CountTableRows).mockResolvedValue(2);
    setupStores();
  });

  it("keeps other pending edits after a row delete succeeds", async () => {
    const user = await renderLoaded();
    // 删除后服务端只剩 id 2、3
    vi.mocked(QueryTableRows).mockResolvedValue({ columns: ["id", "name"], rows: ROWS.slice(1) });

    editCell("2:name", "cyril");
    await waitFor(() => expect(screen.getByText("query.pendingEditsCount")).toBeInTheDocument());

    fireEvent.click(gutter(0));
    fireEvent.contextMenu(gutter(0), { clientX: 20, clientY: 40 });
    await user.click(screen.getByText("query.deleteRecord"));
    await user.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => expect(vi.mocked(QueryTableRows)).toHaveBeenCalled());
    // 编辑跟着 id=3 那一行移到新的行号 1 上,仍未提交。
    await waitFor(() => expect(cell("1:name")?.textContent).toBe("cyril"));
    expect(screen.getByText("query.pendingEditsCount")).toBeInTheDocument();
  });

  it("asks before discarding several pending edits", async () => {
    const user = await renderLoaded();
    editCell("0:name", "ally");
    editCell("1:name", "bobby");

    await user.click(await screen.findByRole("button", { name: "query.discardAll" }));
    // 确认前什么都没丢
    expect(cell("0:name")?.textContent).toBe("ally");
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "query.discardAll" }));
    await waitFor(() => expect(cell("0:name")?.textContent).toBe("ada"));
  });

  it("counts a new row with filled cells as one change", async () => {
    const user = await renderLoaded();
    await user.click(screen.getByTitle("query.addRow"));
    await waitFor(() => expect(cell("3:name")).toBeTruthy());
    editCell("3:id", "4");
    editCell("3:name", "dee");

    await user.click(await screen.findByRole("button", { name: "query.discardAll" }));

    await waitFor(() => expect(cell("3:name")).toBeFalsy());
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("discards a single pending edit without asking", async () => {
    const user = await renderLoaded();
    editCell("0:name", "ally");

    await user.click(await screen.findByRole("button", { name: "query.discardAll" }));

    await waitFor(() => expect(cell("0:name")?.textContent).toBe("ada"));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
});
