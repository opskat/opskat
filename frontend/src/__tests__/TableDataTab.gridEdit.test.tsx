import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import {
  CountTableRows,
  ExecuteSQL,
  GetGridEditStatements,
  OpenTable,
  QueryTableRows,
} from "../../wailsjs/go/query/Query";
import { dbdriver } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, mockGridEditStatements, provideDrivers } from "./dbDriverFixtures";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

/** 预览对话框里展示的语句(对话框把它们以空行隔开放进编辑器)。 */
const previewed = (dialog: HTMLElement) => within(dialog).getByTestId("code-editor").textContent?.split("\n\n");

// 表格编辑的语句由驱动生成:预览与状态栏摘要展示的、确认后执行的,都是 GetGridEditStatements 给的那些。
// 资产用一个只在测试里存在的驱动,证明这条路径不认驱动名。

function setupStores(driver = "fakedb") {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 1, assetName: "db", assetIcon: "", assetType: "database", driver },
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

function openTable(primaryKeys: string[] = ["id"]) {
  vi.mocked(OpenTable).mockResolvedValue(
    JSON.stringify({
      columns: ["id", "name"],
      columnTypes: {},
      columnRules: [],
      primaryKeys,
      totalCount: ROWS.length,
      firstPage: ROWS,
      pageSize: 1000,
    })
  );
}

const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`) as HTMLElement;
const gutter = (rowIdx: number) => document.querySelector(`[data-row-header-key="${rowIdx}"]`) as HTMLElement;
const executed = () => vi.mocked(ExecuteSQL).mock.calls.map(([, sql]) => String(sql));

function editCell(key: string, value: string) {
  fireEvent.doubleClick(cell(key));
  const input = cell(key).querySelector("input") as HTMLInputElement;
  fireEvent.change(input, { target: { value } });
  fireEvent.keyDown(input, { key: "Enter" });
}

async function renderLoaded() {
  const user = userEvent.setup();
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
  await waitFor(() => expect(cell("0:name")).toBeTruthy());
  return user;
}

describe("TableDataTab grid edits go through the driver", () => {
  beforeEach(() => {
    provideDrivers([fakeDriverWith(["grid_edit"])]);
    setupStores();
    for (const fn of [OpenTable, QueryTableRows, CountTableRows, ExecuteSQL, GetGridEditStatements]) {
      vi.mocked(fn).mockReset();
    }
    vi.mocked(toast.error).mockReset();
    vi.mocked(QueryTableRows).mockResolvedValue({ columns: ["id", "name"], rows: ROWS });
    vi.mocked(CountTableRows).mockResolvedValue(ROWS.length);
    openTable();
    mockGridEditStatements();
  });

  it("previews and summarises exactly the statements the driver returned for the pending edits", async () => {
    const user = await renderLoaded();

    await user.click(screen.getByTitle("query.addRow"));
    editCell("2:name", "cy");
    editCell("0:name", "ally");

    const update = 'UPDATE users SET {"name":"ally"} WHERE {"id":1}';
    const insert = 'INSERT users {"name":"cy"}';
    // 状态栏摘要是驱动给的第一条(新增在前)。
    await waitFor(() => expect(screen.getByTitle(insert)).toBeInTheDocument());

    const request = vi.mocked(GetGridEditStatements).mock.calls.at(-1)!;
    expect(request.slice(0, 3)).toEqual([1, "appdb", "users"]);
    expect(request[3]).toEqual(
      dbdriver.GridEditRequest.createFrom({
        columns: ["id", "name"],
        primaryKeys: ["id"],
        inserts: [{ values: [{ column: "name", value: "cy" }] }],
        updates: [{ row: { id: 1, name: "ada" }, set: [{ column: "name", value: "ally" }] }],
        deletes: [],
      })
    );

    await user.click(screen.getByTitle("query.previewSql"));
    const dialog = await screen.findByRole("alertdialog");
    expect(previewed(dialog)).toEqual([insert, update]);
    expect(executed()).toEqual([]);
  });

  it("executes exactly the previewed statements, inserts before updates, one by one", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    const user = await renderLoaded();
    editCell("1:name", "bea");
    await user.click(screen.getByTitle("query.addRow"));
    editCell("2:name", "cy");

    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));

    await waitFor(() =>
      expect(executed()).toEqual(['INSERT users {"name":"cy"}', 'UPDATE users SET {"name":"bea"} WHERE {"id":2}'])
    );
    await waitFor(() => expect(screen.getByTitle("query.applyChanges")).toBeDisabled());
  });

  it("keeps the failed and unmatched edits for a retry and clears only the successful ones", async () => {
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).startsWith("INSERT")) throw new Error("duplicate key");
      if (String(sql).includes('"id":2')) return JSON.stringify({ affected_rows: 0 });
      return JSON.stringify({ affected_rows: 1 });
    });
    const user = await renderLoaded();
    editCell("0:name", "ally");
    editCell("1:name", "bea");
    await user.click(screen.getByTitle("query.addRow"));
    editCell("2:name", "cy");

    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));

    await waitFor(() => expect(executed()).toHaveLength(3));
    await waitFor(() => expect(vi.mocked(QueryTableRows)).toHaveBeenCalled());
    expect(toast.error).toHaveBeenCalledWith("Error: duplicate key");
    // 成功的那一行改动已清掉,刷新后显示服务端的值;没命中的改动与失败的新增行都还在。
    await waitFor(() => expect(cell("0:name")).toHaveTextContent("ada"));
    expect(cell("1:name")).toHaveTextContent("bea");
    expect(cell("2:name")).toHaveTextContent("cy");
    expect(screen.getByTitle("query.applyChanges")).toBeEnabled();

    // 再提交只发剩下的两条。
    vi.mocked(ExecuteSQL).mockClear();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));
    await waitFor(() =>
      expect(executed()).toEqual(['INSERT users {"name":"cy"}', 'UPDATE users SET {"name":"bea"} WHERE {"id":2}'])
    );
  });

  it("keeps a failed new row on the grid when the refreshed page has a different length", async () => {
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).includes('"name":"cy"')) throw new Error("check constraint");
      return JSON.stringify({ affected_rows: 1 });
    });
    // 成功新增的那行出现在刷新后的页里:页变长了一行。
    vi.mocked(QueryTableRows).mockResolvedValue({ columns: ["id", "name"], rows: [...ROWS, { id: 3, name: "dee" }] });
    const user = await renderLoaded();
    await user.click(screen.getByTitle("query.addRow"));
    editCell("2:name", "cy");
    await user.click(screen.getByTitle("query.addRow"));
    editCell("3:name", "dee");

    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));

    await waitFor(() => expect(cell("2:name")).toHaveTextContent("dee"));
    // 失败的新增行排在刷新后的已保存行之后,值仍是用户填的。
    expect(cell("3:name")).toHaveTextContent("cy");
    expect(gutter(4)).toBeNull();
  });

  it("keeps a failed edit on the row it was made on when the refreshed page lists the rows in another order", async () => {
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).includes('"id":2')) throw new Error("check constraint");
      return JSON.stringify({ affected_rows: 1 });
    });
    // 没有排序时服务端可能换了行的次序(PostgreSQL 把改过的行排到后面):刷新后 id 2 在第一行。
    vi.mocked(QueryTableRows).mockResolvedValue({
      columns: ["id", "name"],
      rows: [
        { id: 2, name: "bob" },
        { id: 1, name: "ally" },
      ],
    });
    const user = await renderLoaded();
    editCell("0:name", "ally");
    editCell("1:name", "bea");

    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));

    await waitFor(() => expect(cell("1:name")).toHaveTextContent("ally"));
    // 失败的改动仍在 id 2 那一行上,没有落到刷新后排在原位置的 id 1 上。
    expect(cell("0:name")).toHaveTextContent("bea");

    vi.mocked(ExecuteSQL).mockClear();
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));
    await waitFor(() => expect(executed()).toEqual(['UPDATE users SET {"name":"bea"} WHERE {"id":2}']));
  });

  it("drops a failed edit whose row is no longer on the refreshed page", async () => {
    vi.mocked(ExecuteSQL).mockImplementation(async (_id, sql) => {
      if (String(sql).includes('"id":2')) throw new Error("check constraint");
      return JSON.stringify({ affected_rows: 1 });
    });
    // 刷新后的页里没有 id 2 了(被别处删掉或移到了别的页)。
    vi.mocked(QueryTableRows).mockResolvedValue({
      columns: ["id", "name"],
      rows: [
        { id: 1, name: "ally" },
        { id: 3, name: "cy" },
      ],
    });
    const user = await renderLoaded();
    editCell("0:name", "ally");
    editCell("1:name", "bea");

    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));

    await waitFor(() => expect(cell("1:name")).toHaveTextContent("cy"));
    expect(screen.getByTitle("query.applyChanges")).toBeDisabled();
  });

  it("shows a generation failure where errors show and opens no preview", async () => {
    vi.mocked(GetGridEditStatements).mockRejectedValue('column "name" is not in table users');
    const user = await renderLoaded();
    editCell("0:name", "ally");

    await user.click(screen.getByTitle("query.applyChanges"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('column "name" is not in table users'));
    expect(screen.queryByText("query.confirmExecute")).toBeNull();
    expect(executed()).toEqual([]);
  });

  it("previews and executes the driver's DELETE statements for the selected rows", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    const user = await renderLoaded();

    fireEvent.click(gutter(0));
    fireEvent.click(gutter(1), { ctrlKey: true });
    fireEvent.contextMenu(gutter(1), { clientX: 20, clientY: 40 });
    await user.click(screen.getByText("query.deleteRecords"));

    const dialog = await screen.findByRole("alertdialog");
    expect(previewed(dialog)).toEqual(['DELETE users WHERE {"id":1}', 'DELETE users WHERE {"id":2}']);
    expect(executed()).toEqual([]);

    await user.click(within(dialog).getByText("query.confirmExecute"));
    await waitFor(() => expect(executed()).toEqual(['DELETE users WHERE {"id":1}', 'DELETE users WHERE {"id":2}']));
  });
});

describe("TableDataTab no-primary-key hint follows the driver's declaration", () => {
  beforeEach(() => {
    vi.mocked(OpenTable).mockReset();
    openTable([]);
  });

  const hint = () => screen.queryByText("query.noPrimaryKey");

  it("is hidden for SQLite, which locates a single row by rowid", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    setupStores("sqlite");
    await renderLoaded();
    expect(hint()).toBeNull();
  });

  it("is shown for MySQL, which can only match on all columns", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    setupStores("mysql");
    await renderLoaded();
    expect(hint()).toBeInTheDocument();
  });

  it("is shown for a driver that does not declare exact row location, hidden for one that does", async () => {
    provideDrivers([fakeDriverWith(["grid_edit"])]);
    setupStores();
    const { unmount } = render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="users" />);
    await waitFor(() => expect(hint()).toBeInTheDocument());
    unmount();

    provideDrivers([new dbdriver.Descriptor({ ...fakeDriverWith(["grid_edit"]), locatesRowWithoutPrimaryKey: true })]);
    await renderLoaded();
    expect(hint()).toBeNull();
  });
});
