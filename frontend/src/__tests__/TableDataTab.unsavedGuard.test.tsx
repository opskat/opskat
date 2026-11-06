import { describe, expect, it, beforeEach, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { CountTableRows, ExecuteSQL, OpenTable, QueryTableRows } from "../../wailsjs/go/query/Query";
import type { query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, mockGridEditStatements, provideDrivers } from "./dbDriverFixtures";

// 插值参数也要能断言(条数、行号),t 把参数拼进返回的 key 里;引用保持稳定。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key}(${JSON.stringify(opts)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }) };
});

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
    unsavedTabIds: [],
    pendingCloseTabId: null,
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["orders"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [{ id: "table-1", type: "table", database: "appdb", table: "orders" }],
        activeInnerTabId: "table-1",
        error: null,
      },
    },
  });
}

const BASE_ROWS = [
  { id: 1, status: "pending" },
  { id: 2, status: "paid" },
];

function openTable(rows: Record<string, unknown>[] = BASE_ROWS, totalCount = 2500) {
  vi.mocked(OpenTable).mockResolvedValue(
    JSON.stringify({
      columns: ["id", "status"],
      columnTypes: {},
      columnRules: [],
      primaryKeys: ["id"],
      totalCount,
      firstPage: rows,
      pageSize: 1000,
    })
  );
}

const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`) as HTMLElement;
const dialog = () => screen.queryByRole("alertdialog");
const rowsCalls = () => vi.mocked(QueryTableRows).mock.calls;
const requestedPages = () => rowsCalls().map(([, , , req]) => (req as query_svc.TableRowsRequest).page);

async function renderLoaded() {
  const user = userEvent.setup();
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="orders" />);
  await waitFor(() => expect(cell("0:status")).toBeTruthy());
  return user;
}

function editCell(key: string, value: string) {
  fireEvent.doubleClick(cell(key));
  const input = cell(key).querySelector("input") as HTMLInputElement;
  fireEvent.change(input, { target: { value } });
  fireEvent.keyDown(input, { key: "Enter" });
}

async function openGuardFromNextPage(user: ReturnType<typeof userEvent.setup>) {
  editCell("0:status", "paid");
  await user.click(screen.getByTitle("query.nextPage"));
  return await screen.findByRole("alertdialog");
}

describe("TableDataTab unsaved edits guard", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    mockGridEditStatements();
    setupStores();
    vi.mocked(OpenTable).mockReset();
    vi.mocked(QueryTableRows).mockReset();
    vi.mocked(CountTableRows).mockReset();
    vi.mocked(ExecuteSQL).mockReset();
    vi.mocked(CountTableRows).mockResolvedValue(2500);
    vi.mocked(QueryTableRows).mockResolvedValue({
      columns: ["id", "status"],
      rows: [{ id: 1001, status: "next" }],
    } as query_svc.TableRows);
    openTable();
  });

  it("pages directly without asking when nothing is pending", async () => {
    const user = await renderLoaded();

    await user.click(screen.getByTitle("query.nextPage"));

    await waitFor(() => expect(requestedPages()).toEqual([1]));
    expect(dialog()).toBeNull();
  });

  it("asks before paging away from an edit and summarises it", async () => {
    const user = await renderLoaded();

    const guard = await openGuardFromNextPage(user);

    expect(within(guard).getByText('query.unsavedEditsTitle({"count":1})')).toBeInTheDocument();
    expect(within(guard).getByText('query.unsavedEditsDescPage({"page":2})')).toBeInTheDocument();
    const line = within(guard).getByTestId("unsaved-edit-line");
    expect(line).toHaveTextContent('query.unsavedEditsRow({"row":1})');
    expect(line).toHaveTextContent("status");
    expect(line).toHaveTextContent("pending");
    expect(line).toHaveTextContent("paid");
    expect(rowsCalls()).toHaveLength(0);
  });

  it("stay keeps the edit and does not page", async () => {
    const user = await renderLoaded();
    const guard = await openGuardFromNextPage(user);

    await user.click(within(guard).getByText("query.unsavedEditsStay"));

    await waitFor(() => expect(dialog()).toBeNull());
    expect(rowsCalls()).toHaveLength(0);
    expect(cell("0:status")).toHaveTextContent("paid");
    expect(screen.getByTitle("query.applyChanges")).toBeEnabled();
  });

  it("discard drops the edit and then pages", async () => {
    const user = await renderLoaded();
    const guard = await openGuardFromNextPage(user);

    await user.click(within(guard).getByText("query.unsavedEditsDiscard"));

    await waitFor(() => expect(requestedPages()).toEqual([1]));
    await waitFor(() => expect(cell("0:status")).toHaveTextContent("next"));
    expect(screen.getByTitle("query.applyChanges")).toBeDisabled();
    expect(vi.mocked(ExecuteSQL)).not.toHaveBeenCalled();
  });

  it("submit goes through the SQL preview and pages only after it succeeds", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    const user = await renderLoaded();
    const guard = await openGuardFromNextPage(user);

    await user.click(within(guard).getByText("query.unsavedEditsSubmit"));

    expect(await screen.findByText("query.confirmExecute")).toBeInTheDocument();
    expect(rowsCalls()).toHaveLength(0);

    await user.click(screen.getByText("query.confirmExecute"));

    await waitFor(() => expect(vi.mocked(ExecuteSQL)).toHaveBeenCalledOnce());
    expect(String(vi.mocked(ExecuteSQL).mock.calls[0][1])).toMatch(/^UPDATE/);
    await waitFor(() => expect(requestedPages().at(-1)).toBe(1));
    await waitFor(() => expect(screen.getByTitle("query.applyChanges")).toBeDisabled());
  });

  it("a failed submit stays on the page with the edit kept", async () => {
    vi.mocked(ExecuteSQL).mockRejectedValue(new Error("lock wait timeout"));
    const user = await renderLoaded();
    const guard = await openGuardFromNextPage(user);

    await user.click(within(guard).getByText("query.unsavedEditsSubmit"));
    await user.click(await screen.findByText("query.confirmExecute"));

    await waitFor(() => expect(vi.mocked(ExecuteSQL)).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.queryByText("query.confirmExecute")).toBeNull());
    expect(requestedPages()).not.toContain(1);
    expect(cell("0:status")).toHaveTextContent("paid");
    expect(screen.getByTitle("query.applyChanges")).toBeEnabled();
  });

  it("cancelling the preview stays put and forgets the pending page change", async () => {
    vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
    const user = await renderLoaded();
    const guard = await openGuardFromNextPage(user);

    await user.click(within(guard).getByText("query.unsavedEditsSubmit"));
    await user.click(await screen.findByText("action.cancel"));

    await waitFor(() => expect(screen.queryByText("query.confirmExecute")).toBeNull());
    expect(rowsCalls()).toHaveLength(0);
    expect(cell("0:status")).toHaveTextContent("paid");

    // 之后单独提交一次,不应再顺带翻到第 2 页。
    await user.click(screen.getByTitle("query.applyChanges"));
    await user.click(await screen.findByText("query.confirmExecute"));
    await waitFor(() => expect(vi.mocked(ExecuteSQL)).toHaveBeenCalledOnce());
    await waitFor(() => expect(rowsCalls().length).toBeGreaterThan(0));
    expect(requestedPages()).not.toContain(1);
  });

  const triggers: [string, string, (user: ReturnType<typeof userEvent.setup>) => Promise<void>][] = [
    [
      "changing the page size",
      'query.unsavedEditsDescPageSize({"size":500})',
      async (user) => {
        await user.click(screen.getByTitle("query.tableFooterSettings"));
        const limitInput = screen.getByLabelText("query.pageSize");
        await user.clear(limitInput);
        await user.type(limitInput, "500{Enter}");
      },
    ],
    [
      "sorting from the column header",
      "query.unsavedEditsDescSort",
      async (user) => {
        fireEvent.click(screen.getByTitle("query.columnActions:status"));
        await user.click(screen.getByText("query.sortDesc"));
      },
    ],
    [
      "applying the filter builder",
      "query.unsavedEditsDescFilter",
      async (user) => {
        await user.click(screen.getByTitle("query.filterSort"));
        await user.click(screen.getByText("query.applyFilterSort"));
      },
    ],
    [
      "filtering by a cell's value",
      "query.unsavedEditsDescFilter",
      async (user) => {
        fireEvent.contextMenu(cell("1:status"), { clientX: 40, clientY: 50 });
        await user.hover(screen.getByText("query.filter"));
        await user.click(screen.getByText("query.filterOperatorIsNot"));
      },
    ],
    [
      "refreshing",
      "query.unsavedEditsDescRefresh",
      async (user) => {
        await user.click(screen.getByTitle(/^query\.refreshTable/));
      },
    ],
    [
      "refreshing with the shortcut",
      "query.unsavedEditsDescRefresh",
      async () => {
        fireEvent.keyDown(window, { key: "r", code: "KeyR", metaKey: true, ctrlKey: true });
      },
    ],
  ];

  it.each(triggers)("asks before %s and runs it after discarding", async (_name, description, trigger) => {
    const user = await renderLoaded();
    editCell("0:status", "paid");

    await trigger(user);

    const guard = await screen.findByRole("alertdialog");
    expect(within(guard).getByText(description)).toBeInTheDocument();
    expect(rowsCalls()).toHaveLength(0);

    await user.click(within(guard).getByText("query.unsavedEditsDiscard"));

    await waitFor(() => expect(rowsCalls().length).toBeGreaterThan(0));
    expect(screen.getByTitle("query.applyChanges")).toBeDisabled();
  });

  it("stay after a page size change shows the current page size again, not the rejected draft", async () => {
    const user = await renderLoaded();
    editCell("0:status", "paid");
    await user.click(screen.getByTitle("query.tableFooterSettings"));
    const limitInput = screen.getByLabelText("query.pageSize");
    await user.clear(limitInput);
    await user.type(limitInput, "200{Enter}");

    const guard = await screen.findByRole("alertdialog");
    await user.click(within(guard).getByText("query.unsavedEditsStay"));
    await waitFor(() => expect(dialog()).toBeNull());

    expect(screen.getByLabelText("query.pageSize")).toHaveValue("1000");
    expect(rowsCalls()).toHaveLength(0);
    expect(cell("0:status")).toHaveTextContent("paid");
  });

  it("folds the summary after ten lines into a count of the rest", async () => {
    // 网格是虚拟滚动的,jsdom 里只渲染前几行:4 行 × 2 列的修改 + 4 个新增行 = 12 条。
    openTable(Array.from({ length: 4 }, (_, i) => ({ id: i + 1, status: "pending" })));
    const user = await renderLoaded();
    for (let i = 0; i < 4; i++) {
      editCell(`${i}:id`, String(100 + i));
      editCell(`${i}:status`, "paid");
    }
    for (let i = 0; i < 4; i++) {
      await user.click(screen.getByTitle("query.addRow"));
      fireEvent.keyDown(cell(`${4 + i}:id`).querySelector("input") as HTMLInputElement, { key: "Escape" });
    }

    await user.click(screen.getByTitle("query.nextPage"));
    const guard = await screen.findByRole("alertdialog");

    expect(within(guard).getByText('query.unsavedEditsTitle({"count":12})')).toBeInTheDocument();
    expect(within(guard).getAllByTestId("unsaved-edit-line")).toHaveLength(10);
    expect(within(guard).getByText('query.unsavedEditsMore({"count":2})')).toBeInTheDocument();
  });

  it("summarises a new row on a single line", async () => {
    const user = await renderLoaded();
    await user.click(screen.getByTitle("query.addRow"));
    const newRowInput = cell("2:id").querySelector("input") as HTMLInputElement;
    fireEvent.change(newRowInput, { target: { value: "99" } });
    fireEvent.keyDown(newRowInput, { key: "Enter" });

    await user.click(screen.getByTitle("query.nextPage"));
    const guard = await screen.findByRole("alertdialog");

    expect(within(guard).getByText('query.unsavedEditsTitle({"count":1})')).toBeInTheDocument();
    const line = within(guard).getByTestId("unsaved-edit-line");
    expect(line).toHaveTextContent('query.unsavedEditsRow({"row":3})');
    expect(line).toHaveTextContent("query.unsavedEditsNewRow");
    expect(line).toHaveTextContent("id");
    expect(line).toHaveTextContent("99");
  });

  describe("closing the tab", () => {
    const innerIds = () => useQueryStore.getState().dbStates["query-1"]?.innerTabs.map((it) => it.id);
    const requestClose = () => act(() => useQueryStore.getState().closeInnerTab("query-1", "table-1"));

    it("closes directly when nothing is pending", async () => {
      await renderLoaded();
      requestClose();
      expect(innerIds()).toEqual([]);
      expect(dialog()).toBeNull();
    });

    it("an edit marks the database tab as unsaved", async () => {
      await renderLoaded();
      editCell("0:status", "paid");
      await waitFor(() => expect(useTabStore.getState().unsavedTabIds).toContain("query-1"));
    });

    it("asks before closing an edited tab; stay keeps the tab and the edit", async () => {
      const user = await renderLoaded();
      editCell("0:status", "paid");
      requestClose();

      const guard = await screen.findByRole("alertdialog");
      expect(within(guard).getByText("query.unsavedEditsDescClose")).toBeInTheDocument();
      expect(within(guard).getByText('query.unsavedEditsTitle({"count":1})')).toBeInTheDocument();
      await user.click(within(guard).getByText("query.unsavedEditsStay"));

      await waitFor(() => expect(dialog()).toBeNull());
      expect(innerIds()).toEqual(["table-1"]);
      expect(cell("0:status")).toHaveTextContent("paid");
      expect(useQueryStore.getState().dbStates["query-1"].closeRequest).toBeUndefined();
    });

    it("「放弃并继续」 closes the tab without submitting", async () => {
      const user = await renderLoaded();
      editCell("0:status", "paid");
      requestClose();

      await user.click(within(await screen.findByRole("alertdialog")).getByText("query.unsavedEditsDiscard"));

      expect(innerIds()).toEqual([]);
      expect(vi.mocked(ExecuteSQL)).not.toHaveBeenCalled();
    });

    it("submit closes the tab only after the submit succeeds", async () => {
      vi.mocked(ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));
      const user = await renderLoaded();
      editCell("0:status", "paid");
      requestClose();

      await user.click(within(await screen.findByRole("alertdialog")).getByText("query.unsavedEditsSubmit"));
      await screen.findByText("query.confirmExecute");
      expect(innerIds()).toEqual(["table-1"]);
      await user.click(screen.getByText("query.confirmExecute"));

      await waitFor(() => expect(innerIds()).toEqual([]));
      expect(vi.mocked(ExecuteSQL)).toHaveBeenCalledOnce();
    });

    it("a failed submit keeps the tab open with the edit", async () => {
      vi.mocked(ExecuteSQL).mockRejectedValue(new Error("lock wait timeout"));
      const user = await renderLoaded();
      editCell("0:status", "paid");
      requestClose();

      await user.click(within(await screen.findByRole("alertdialog")).getByText("query.unsavedEditsSubmit"));
      await user.click(await screen.findByText("query.confirmExecute"));

      await waitFor(() => expect(screen.queryByText("query.confirmExecute")).toBeNull());
      expect(innerIds()).toEqual(["table-1"]);
      expect(cell("0:status")).toHaveTextContent("paid");
    });

    it("closing the database tab with this as the only edited tab closes it after discarding", async () => {
      const user = await renderLoaded();
      editCell("0:status", "paid");
      act(() => useQueryStore.getState().requestCloseInnerTabs("query-1", ["table-1"], { closeOuter: true }));

      await user.click(within(await screen.findByRole("alertdialog")).getByText("query.unsavedEditsDiscard"));

      expect(useTabStore.getState().tabs).toEqual([]);
    });
  });
});
