import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { CountTableRows, OpenTable, QueryTableRows } from "../../wailsjs/go/query/Query";
import type { query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

// 插值参数也要能断言(库.表),t 把参数拼进返回的 key 里;引用保持稳定。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key}(${JSON.stringify(opts)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }) };
});

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value?: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

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

function openTable(totalCount: number) {
  vi.mocked(OpenTable).mockResolvedValue(
    JSON.stringify({
      columns: ["id", "name"],
      columnTypes: {},
      columnRules: [],
      primaryKeys: ["id"],
      totalCount,
      firstPage: [
        { id: 1, name: "青木贸易" },
        { id: 2, name: "北岭物流" },
      ],
      pageSize: 1000,
    })
  );
}

const cell = (key: string) => document.querySelector(`[data-cell-key="${key}"]`);
const errorCard = () => screen.queryByRole("alert");
const TITLE = 'query.loadTableFailed({"table":"appdb.orders"})';

async function renderLoaded() {
  const user = userEvent.setup();
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="orders" />);
  await waitFor(() => expect(cell("1:name")?.textContent).toBe("北岭物流"));
  return user;
}

describe("TableDataTab load failure", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    setupStores();
    vi.mocked(OpenTable).mockReset();
    vi.mocked(QueryTableRows).mockReset();
    vi.mocked(CountTableRows).mockReset();
    vi.mocked(CountTableRows).mockResolvedValue(2);
    openTable(2);
  });

  it("shows a card with the selectable raw error, the status bar failure and no filter shortcut", async () => {
    const user = await renderLoaded();
    vi.mocked(QueryTableRows).mockRejectedValueOnce("Error 1146: table 'orders' doesn't exist");

    await user.click(screen.getByTitle(/^query\.refreshTable/));

    await waitFor(() => expect(errorCard()).toBeInTheDocument());
    expect(screen.getByText(TITLE)).toBeInTheDocument();
    const raw = screen.getByText("Error 1146: table 'orders' doesn't exist");
    expect(raw.className).toContain("select-text");
    expect(screen.getByText("query.loadFailed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "query.retry" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "query.copyError" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "query.editFilter" })).toBeNull();
  });

  it("retries the same request and updates the card when it fails again", async () => {
    const user = await renderLoaded();
    vi.mocked(QueryTableRows).mockRejectedValueOnce("first failure");
    await user.click(screen.getByTitle(/^query\.refreshTable/));
    await screen.findByText("first failure");
    const callsBefore = vi.mocked(QueryTableRows).mock.calls.length;

    vi.mocked(QueryTableRows).mockRejectedValueOnce("second failure");
    await user.click(screen.getByRole("button", { name: "query.retry" }));

    await screen.findByText("second failure");
    expect(screen.queryByText("first failure")).toBeNull();
    expect(vi.mocked(QueryTableRows).mock.calls.length).toBe(callsBefore + 1);
    expect(vi.mocked(QueryTableRows).mock.calls.at(-1)![3]).toEqual(vi.mocked(QueryTableRows).mock.calls.at(-2)![3]);

    vi.mocked(QueryTableRows).mockResolvedValueOnce({
      columns: ["id", "name"],
      rows: [{ id: 3, name: "云栖科技" }],
    } as query_svc.TableRows);
    await user.click(screen.getByRole("button", { name: "query.retry" }));
    await waitFor(() => expect(cell("0:name")?.textContent).toBe("云栖科技"));
    expect(errorCard()).toBeNull();
    expect(screen.queryByText("query.loadFailed")).toBeNull();
  });

  it("copies the raw error text", async () => {
    const user = await renderLoaded();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.mocked(QueryTableRows).mockRejectedValueOnce("boom: raw text");
    await user.click(screen.getByTitle(/^query\.refreshTable/));
    await screen.findByText("boom: raw text");

    await user.click(screen.getByRole("button", { name: "query.copyError" }));

    expect(writeText).toHaveBeenCalledWith("boom: raw text");
  });

  it("offers editing the filter only while one is applied, and that opens the builder", async () => {
    const user = await renderLoaded();
    vi.mocked(QueryTableRows).mockResolvedValueOnce({
      columns: ["id", "name"],
      rows: [{ id: 2, name: "北岭物流" }],
    } as query_svc.TableRows);
    fireEvent.contextMenu(cell("1:name")!, { clientX: 40, clientY: 40 });
    fireEvent.click(screen.getByText("query.filterOperatorIs"));
    await waitFor(() => expect(cell("0:name")?.textContent).toBe("北岭物流"));
    await user.click(screen.getByTitle("query.filterSort"));
    expect(screen.queryByText("query.filterBuilderTitle")).toBeNull();

    vi.mocked(QueryTableRows).mockRejectedValueOnce("bad filter");
    await user.click(screen.getByTitle(/^query\.refreshTable/));
    await screen.findByText("bad filter");
    await user.click(screen.getByRole("button", { name: "query.editFilter" }));

    expect(screen.getByText("query.filterBuilderTitle")).toBeInTheDocument();
  });

  it("shows the card in the JSON view too", async () => {
    const user = await renderLoaded();
    await user.click(screen.getByRole("radio", { name: "query.jsonView" }));
    vi.mocked(QueryTableRows).mockRejectedValueOnce("json view failure");

    await user.click(screen.getByTitle(/^query\.refreshTable/));

    await screen.findByText("json view failure");
    expect(screen.getByText(TITLE)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "query.retry" })).toBeInTheDocument();
  });

  it("retries a failed first open by opening the table again", async () => {
    vi.mocked(OpenTable).mockReset();
    vi.mocked(OpenTable).mockRejectedValueOnce("connection lost");
    const user = userEvent.setup();
    render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="orders" />);
    await screen.findByText("connection lost");
    expect(screen.getByText("query.loadFailed")).toBeInTheDocument();
    openTable(2);

    await user.click(screen.getByRole("button", { name: "query.retry" }));

    await waitFor(() => expect(cell("1:name")?.textContent).toBe("北岭物流"));
    expect(errorCard()).toBeNull();
    expect(vi.mocked(OpenTable)).toHaveBeenCalledTimes(2);
  });
});
