import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataTab } from "@/components/query/TableDataTab";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { CountTableRows, OpenTable, QueryTableRows } from "../../wailsjs/go/query/Query";
import type { query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

// 插值参数也要能断言(第几页、耗时),t 把参数拼进返回的 key 里;引用保持稳定。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key}(${JSON.stringify(opts)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }) };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

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
const header = (col: string) => document.querySelector(`[data-column-header-key="${col}"]`);
const elapsedText = () => screen.queryByText(/query\.elapsedMs\(\{"ms":\d+\}\)/);

async function renderLoaded() {
  const user = userEvent.setup();
  render(<TableDataTab tabId="query-1" innerTabId="table-1" database="appdb" table="orders" />);
  await waitFor(() => expect(cell("1:name")?.textContent).toBe("北岭物流"));
  return user;
}

describe("TableDataTab reload", () => {
  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
    setupStores();
    vi.mocked(OpenTable).mockReset();
    vi.mocked(QueryTableRows).mockReset();
    vi.mocked(CountTableRows).mockReset();
    vi.mocked(CountTableRows).mockResolvedValue(2);
    openTable(2);
  });

  it("keeps the old rows while refreshing, says which page is loading, then shows the fetch time", async () => {
    const user = await renderLoaded();
    expect(elapsedText()).toBeInTheDocument();

    const next = deferred<query_svc.TableRows>();
    vi.mocked(QueryTableRows).mockReturnValueOnce(next.promise);
    await user.click(screen.getByTitle(/^query\.refreshTable/));

    expect(cell("1:name")?.textContent).toBe("北岭物流");
    expect(screen.getByRole("progressbar")).toBeInTheDocument();
    expect(screen.getByText('query.loadingPage({"page":1})')).toBeInTheDocument();
    expect(screen.getByTitle("query.stopLoading")).toBeEnabled();
    expect(elapsedText()).toBeNull();

    next.resolve({ columns: ["id", "name"], rows: [{ id: 3, name: "云栖科技" }] } as query_svc.TableRows);

    await waitFor(() => expect(cell("0:name")?.textContent).toBe("云栖科技"));
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(screen.queryByText(/query\.loadingPage/)).toBeNull();
    expect(elapsedText()).toBeInTheDocument();
  });

  it("names the page being fetched when paging forward", async () => {
    openTable(2500);
    const user = await renderLoaded();
    vi.mocked(QueryTableRows).mockReturnValueOnce(deferred<query_svc.TableRows>().promise);

    await user.click(screen.getByTitle("query.nextPage"));

    expect(screen.getByText('query.loadingPage({"page":2})')).toBeInTheDocument();
    expect(cell("0:name")?.textContent).toBe("青木贸易");
  });

  it("stop restores the normal display of the old rows", async () => {
    const user = await renderLoaded();
    vi.mocked(QueryTableRows).mockReturnValueOnce(deferred<query_svc.TableRows>().promise);
    await user.click(screen.getByTitle(/^query\.refreshTable/));
    expect(screen.getByRole("progressbar")).toBeInTheDocument();

    await user.click(screen.getByTitle("query.stopLoading"));

    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(screen.queryByText(/query\.loadingPage/)).toBeNull();
    expect(cell("1:name")?.textContent).toBe("北岭物流");
    expect(elapsedText()).toBeInTheDocument();
  });

  it("keeps hidden columns while the column names stay the same and shows all columns once they change", async () => {
    const user = await renderLoaded();
    fireEvent.contextMenu(header("name")!, { clientX: 40, clientY: 20 });
    fireEvent.click(screen.getByText("query.hideColumn"));
    expect(header("name")).toBeNull();

    vi.mocked(QueryTableRows).mockResolvedValueOnce({
      columns: ["id", "name"],
      rows: [{ id: 3, name: "云栖科技" }],
    } as query_svc.TableRows);
    await user.click(screen.getByTitle(/^query\.refreshTable/));
    await waitFor(() => expect(cell("0:id")?.textContent).toBe("3"));
    expect(header("name")).toBeNull();

    vi.mocked(QueryTableRows).mockResolvedValueOnce({
      columns: ["id", "name", "region"],
      rows: [{ id: 4, name: "北岭仓", region: "华南" }],
    } as query_svc.TableRows);
    await user.click(screen.getByTitle(/^query\.refreshTable/));
    await waitFor(() => expect(cell("0:id")?.textContent).toBe("4"));
    expect(header("name")).toBeTruthy();
    expect(header("region")).toBeTruthy();
  });
});
