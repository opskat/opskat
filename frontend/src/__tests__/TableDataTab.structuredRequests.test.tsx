import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { CountTableRows, ExecuteSQL, ExecuteSQLPaged, OpenTable, QueryTableRows } from "../../wailsjs/go/query/Query";
import { FAKE_DRIVER, provideDrivers } from "./dbDriverFixtures";

// 表浏览(打开、翻页、改每页行数、计数、筛选、排序)发给后端的是结构化请求,由资产的驱动
// 回答。这里用一个前端一无所知的驱动名:任何一步都不该、也没法为它拼 SQL。

const OPENED_ROWS = [
  { id: 1, name: "alice" },
  { id: 2, name: "bob" },
];

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
          driver: FAKE_DRIVER.id,
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

async function renderOpened() {
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
  await waitFor(() => expect(screen.getByText("alice")).toBeInTheDocument());
}

const lastRowsCall = () => vi.mocked(QueryTableRows).mock.calls.at(-1);
const lastCountCall = () => vi.mocked(CountTableRows).mock.calls.at(-1);

function expectNoSqlSent() {
  expect(vi.mocked(ExecuteSQL)).not.toHaveBeenCalled();
  expect(vi.mocked(ExecuteSQLPaged)).not.toHaveBeenCalled();
}

describe("TableDataTab structured table requests", () => {
  beforeEach(() => {
    provideDrivers([FAKE_DRIVER]);
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(ExecuteSQLPaged).mockReset();
    vi.mocked(OpenTable).mockReset();
    vi.mocked(QueryTableRows).mockReset();
    vi.mocked(CountTableRows).mockReset();
    vi.mocked(OpenTable).mockResolvedValue(
      JSON.stringify({
        columns: ["id", "name"],
        columnTypes: { id: "int", name: "text" },
        columnRules: [],
        primaryKeys: ["id"],
        totalCount: 2500,
        firstPage: OPENED_ROWS,
        pageSize: 1000,
      })
    );
    vi.mocked(QueryTableRows).mockResolvedValue({ columns: ["id", "name"], rows: [{ id: 9, name: "from-driver" }] });
    vi.mocked(CountTableRows).mockResolvedValue(77);
    setupStores();
  });

  it("opens the table with one structured request and shows the driver's first screen", async () => {
    await renderOpened();

    expect(vi.mocked(OpenTable).mock.calls).toEqual([[1, "appdb", "users", 1000]]);
    expect(screen.getByText("bob")).toBeInTheDocument();
    // 首屏已经带了第一页与总行数,不再另外取数或计数。
    expect(vi.mocked(QueryTableRows)).not.toHaveBeenCalled();
    expect(vi.mocked(CountTableRows)).not.toHaveBeenCalled();
    expectNoSqlSent();
  });

  it("pages by asking the driver for the page and presents what it returns", async () => {
    const user = userEvent.setup();
    await renderOpened();

    await user.click(screen.getByTitle("query.nextPage"));

    await waitFor(() => expect(screen.getByText("from-driver")).toBeInTheDocument());
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
    expect(lastRowsCall()).toEqual([1, "appdb", "users", { filters: [], sorts: [], page: 1, pageSize: 1000 }]);
    expectNoSqlSent();
  });

  it("refreshes by asking the driver for the current page and the count", async () => {
    const user = userEvent.setup();
    await renderOpened();

    await user.click(screen.getByTitle(/^query\.refreshTable/));

    await waitFor(() => expect(screen.getByText("from-driver")).toBeInTheDocument());
    expect(lastRowsCall()).toEqual([1, "appdb", "users", { filters: [], sorts: [], page: 0, pageSize: 1000 }]);
    expect(lastCountCall()).toEqual([1, "appdb", "users", []]);
    expectNoSqlSent();
  });

  it("keeps the rows and drops only the total when the count fails", async () => {
    const user = userEvent.setup();
    vi.mocked(CountTableRows).mockRejectedValue(new Error("count: permission denied"));
    await renderOpened();
    // 总行数已知时才有「末页」按钮。
    expect(screen.getByTitle("query.lastPage")).toBeInTheDocument();

    await user.click(screen.getByTitle(/^query\.refreshTable/));

    await waitFor(() => expect(screen.getByText("from-driver")).toBeInTheDocument());
    await waitFor(() => expect(screen.queryByTitle("query.lastPage")).not.toBeInTheDocument());
    expect(screen.queryByText(/permission denied/)).not.toBeInTheDocument();
  });

  it("shows the driver's error unchanged when a page fails", async () => {
    const user = userEvent.setup();
    vi.mocked(QueryTableRows).mockRejectedValue('SQL query failed: relation "users" does not exist');
    await renderOpened();

    await user.click(screen.getByTitle("query.nextPage"));

    await waitFor(() =>
      expect(screen.getByText('SQL query failed: relation "users" does not exist')).toBeInTheDocument()
    );
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
  });

  it("filters by a cell's value with a structured condition", async () => {
    const user = userEvent.setup();
    await renderOpened();

    fireEvent.contextMenu(document.querySelector('[data-cell-key="1:name"]') as HTMLElement, {
      clientX: 40,
      clientY: 50,
    });
    await user.hover(screen.getByText("query.filter"));
    await user.click(screen.getByText("query.filterOperatorIsNot"));

    const filters = [{ kind: "condition", join: "and", disabled: false, column: "name", operator: "!=", value: "bob" }];
    await waitFor(() =>
      expect(lastRowsCall()).toEqual([1, "appdb", "users", { filters, sorts: [], page: 0, pageSize: 1000 }])
    );
    await waitFor(() => expect(lastCountCall()).toEqual([1, "appdb", "users", filters]));
    expectNoSqlSent();
  });

  it("filters by a cell's value as a range of that value for the between operators", async () => {
    const user = userEvent.setup();
    await renderOpened();

    fireEvent.contextMenu(document.querySelector('[data-cell-key="0:id"]') as HTMLElement, {
      clientX: 40,
      clientY: 50,
    });
    await user.hover(screen.getByText("query.filter"));
    await user.click(screen.getByText("query.filterOperatorIsBetween"));

    await waitFor(() =>
      expect(lastCountCall()).toEqual([
        1,
        "appdb",
        "users",
        [{ kind: "condition", join: "and", disabled: false, column: "id", operator: "between", value: [1, 1] }],
      ])
    );
    expectNoSqlSent();
  });

  it("sorts from the column header with a structured sort item", async () => {
    const user = userEvent.setup();
    await renderOpened();

    fireEvent.click(screen.getByTitle("query.columnActions:name"));
    await user.click(screen.getByText("query.sortDesc"));

    await waitFor(() =>
      expect(lastRowsCall()).toEqual([
        1,
        "appdb",
        "users",
        { filters: [], sorts: [{ column: "name", dir: "desc" }], page: 0, pageSize: 1000 },
      ])
    );
    expectNoSqlSent();
  });

  it("applies the filter builder's conditions, groups and sort criteria as structured items", async () => {
    const user = userEvent.setup();
    await renderOpened();

    await user.click(screen.getByTitle("query.filterSort"));
    // 第一个条件落在第一列 id 上;从建议值里选 2。
    await user.click(screen.getAllByTitle("query.addFilter")[0]);
    await user.click(screen.getByTitle("query.chooseFilterValue"));
    await user.click(within(await screen.findByRole("dialog")).getByText("2"));
    await user.keyboard("{Escape}");
    // 第二个条件(落在 name 上)不填值;再加一个空的组和一个排序项。
    await user.click(screen.getAllByTitle("query.addFilter")[0]);
    await user.click(screen.getAllByTitle("query.addFilterGroup")[0]);
    await user.click(screen.getByTitle("query.addSort"));
    expect(vi.mocked(QueryTableRows)).not.toHaveBeenCalled();

    await user.click(screen.getByText("query.applyFilterSort"));

    const filters = [
      { kind: "condition", join: "and", disabled: false, column: "id", operator: "=", value: 2 },
      { kind: "condition", join: "and", disabled: false, column: "name", operator: "=", unset: true },
      { kind: "group", join: "and", disabled: false, negated: false, items: [] },
    ];
    await waitFor(() =>
      expect(lastRowsCall()).toEqual([
        1,
        "appdb",
        "users",
        { filters, sorts: [{ column: "id", dir: "asc" }], page: 0, pageSize: 1000 },
      ])
    );
    await waitFor(() => expect(lastCountCall()).toEqual([1, "appdb", "users", filters]));
    expectNoSqlSent();
  });
});
