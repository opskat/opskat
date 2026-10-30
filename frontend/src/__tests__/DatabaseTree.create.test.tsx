import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { DatabaseTree } from "@/components/query/DatabaseTree";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { ListDatabaseObjects } from "../../wailsjs/go/query/Query";
import { asset_entity, query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

// 带插值参数的文案要能看出参数(全局 mock 只回键名)。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key} ${Object.values(opts).join(" ")}` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }), initReactI18next: { type: "3rdParty", init: vi.fn() } };
});

// 新建表的入口:「表 (n)」分组行的 +、空库的「新建表」、库 / 表分组 / 表的右键菜单、侧栏工具栏 + 下拉;
// 都打开以触发处所在的库(有 schema 的驱动为所在 schema)为目标的新建表标签。驱动没声明建表或资产是只读连接
// 时一个都不出现。

const TAB = "query-1";

function setup(
  driver: string,
  opts: {
    tables?: Record<string, string[]>;
    expandedDbs?: string[];
    defaultDatabase?: string;
    readOnly?: boolean;
  } = {}
) {
  useTabStore.setState({
    tabs: [
      {
        id: TAB,
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "db",
          assetIcon: "",
          assetType: "database",
          driver,
          defaultDatabase: opts.defaultDatabase ?? "",
        },
      },
    ],
    activeTabId: TAB,
  });
  useAssetStore.setState({
    assets: [
      new asset_entity.Asset({
        ID: 1,
        Name: "db",
        Type: "database",
        Config: JSON.stringify({ driver, read_only: !!opts.readOnly }),
      }),
    ],
  });
  const tables = opts.tables ?? { appdb: ["users", "logs"], emptydb: [], other: ["t1"] };
  useQueryStore.setState({
    dbStates: {
      [TAB]: {
        databases: Object.keys(tables),
        tables,
        loadingTables: {},
        expandedDbs: opts.expandedDbs ?? Object.keys(tables),
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [],
        activeInnerTabId: null,
        error: null,
      },
    },
    redisStates: {},
    mongoStates: {},
  });
}

async function renderTree() {
  await act(async () => {
    render(<DatabaseTree tabId={TAB} />);
  });
}

/** 打开的新建表标签的目标(只看最后打开的那个)。 */
function createdTarget() {
  const state = useQueryStore.getState().dbStates[TAB];
  const tab = state.innerTabs.find((t) => t.id === state.activeInnerTabId);
  if (tab?.type !== "designer" || tab.mode !== "create") return null;
  return { database: tab.database, schema: tab.schema };
}

/** 「表 (n)」分组行(按它所在的库)。 */
function groupRow(db: string): HTMLElement {
  return screen.getByTestId(`tree-table-group-${db}`);
}

function openNewMenu() {
  fireEvent.pointerDown(screen.getByTestId("database-new-menu"), { button: 0, ctrlKey: false });
}

beforeEach(() => {
  vi.mocked(ListDatabaseObjects).mockReset();
  vi.mocked(ListDatabaseObjects).mockResolvedValue(new query_svc.DatabaseObjects({}));
});

describe("DatabaseTree 新建表入口", () => {
  beforeEach(() => provideDrivers(BUILTIN_DRIVERS));

  it("「表 (n)」分组行的 +:在该库新建表", async () => {
    setup("mysql");
    await renderTree();
    fireEvent.click(within(groupRow("other")).getByTestId("tree-table-group-new-table"));
    expect(createdTarget()).toEqual({ database: "other", schema: undefined });
  });

  it("空库「暂无表」旁的「新建表」:在该库新建表", async () => {
    setup("mysql");
    await renderTree();
    expect(screen.getAllByTestId("tree-empty-new-table")).toHaveLength(1);
    fireEvent.click(screen.getByTestId("tree-empty-new-table"));
    expect(createdTarget()).toEqual({ database: "emptydb", schema: undefined });
  });

  it("库节点的右键菜单「新建表」", async () => {
    setup("mysql");
    await renderTree();
    fireEvent.contextMenu(screen.getByText("other"));
    fireEvent.click(await screen.findByTestId("database-add-table"));
    expect(createdTarget()).toEqual({ database: "other", schema: undefined });
  });

  it("「表」分组的右键菜单「新建表」", async () => {
    setup("mysql");
    await renderTree();
    fireEvent.contextMenu(groupRow("appdb"));
    fireEvent.click(await screen.findByTestId("table-group-new-table"));
    expect(createdTarget()).toEqual({ database: "appdb", schema: undefined });
  });

  it("表的右键菜单「新建表」:建在这张表所在的库", async () => {
    setup("mysql");
    await renderTree();
    fireEvent.contextMenu(screen.getByText("t1"));
    fireEvent.click(await screen.findByTestId("table-new-table"));
    expect(createdTarget()).toEqual({ database: "other", schema: undefined });
  });

  it("有 schema 的驱动:表的右键菜单建在这张表所在的 schema", async () => {
    setup("postgresql", { tables: { appdb: ["sales.orders"] } });
    useQueryStore.setState((s) => ({
      dbStates: { [TAB]: { ...s.dbStates[TAB], expandedSchemas: { appdb: ["sales"] } } },
    }));
    await renderTree();
    fireEvent.contextMenu(screen.getByText("orders"));
    fireEvent.click(await screen.findByTestId("table-new-table"));
    expect(createdTarget()).toEqual({ database: "appdb", schema: "sales" });
  });
});

describe("DatabaseTree 侧栏工具栏的新建下拉", () => {
  beforeEach(() => provideDrivers(BUILTIN_DRIVERS));

  it("下拉里有新建表(注明目标库)、新建查询与新建数据库…(驱动声明了建库)", async () => {
    setup("mysql");
    await renderTree();
    openNewMenu();
    expect(await screen.findByTestId("database-new-menu-table")).toHaveTextContent("appdb");
    expect(screen.getByTestId("database-new-menu-sql")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("database-new-menu-database"));
    expect(await screen.findByText("query.createDatabaseDialogTitle")).toBeInTheDocument();
  });

  it("新建查询打开 SQL 标签", async () => {
    setup("mysql");
    await renderTree();
    openNewMenu();
    fireEvent.click(await screen.findByTestId("database-new-menu-sql"));
    const state = useQueryStore.getState().dbStates[TAB];
    expect(state.innerTabs.map((t) => t.type)).toEqual(["sql"]);
  });

  it("驱动没声明建库:没有「新建数据库…」", async () => {
    setup("sqlite");
    await renderTree();
    openNewMenu();
    await screen.findByTestId("database-new-menu-table");
    expect(screen.queryByTestId("database-new-menu-database")).not.toBeInTheDocument();
  });

  it("目标:选中的表所在的库", async () => {
    setup("mysql");
    await renderTree();
    fireEvent.click(screen.getByText("t1"));
    openNewMenu();
    fireEvent.click(await screen.findByTestId("database-new-menu-table"));
    expect(createdTarget()).toEqual({ database: "other", schema: undefined });
  });

  it("目标:没有选中时取第一个展开的库", async () => {
    setup("mysql", { expandedDbs: ["emptydb", "other"] });
    await renderTree();
    openNewMenu();
    fireEvent.click(await screen.findByTestId("database-new-menu-table"));
    expect(createdTarget()).toEqual({ database: "emptydb", schema: undefined });
  });

  it("目标:没有展开的库时取资产的默认库", async () => {
    setup("mysql", { expandedDbs: [], defaultDatabase: "other" });
    await renderTree();
    openNewMenu();
    fireEvent.click(await screen.findByTestId("database-new-menu-table"));
    expect(createdTarget()).toEqual({ database: "other", schema: undefined });
  });
});

describe("DatabaseTree 不能新建表时入口都不出现", () => {
  async function expectNoCreateEntries() {
    await renderTree();
    expect(screen.queryByTestId("tree-table-group-new-table")).not.toBeInTheDocument();
    expect(screen.queryByTestId("tree-empty-new-table")).not.toBeInTheDocument();
    fireEvent.contextMenu(screen.getByText("other"));
    expect(screen.queryByTestId("database-add-table")).not.toBeInTheDocument();
    fireEvent.keyDown(document.body, { key: "Escape" });
    fireEvent.contextMenu(groupRow("appdb"));
    expect(screen.queryByTestId("table-group-new-table")).not.toBeInTheDocument();
    fireEvent.keyDown(document.body, { key: "Escape" });
    fireEvent.contextMenu(screen.getByText("t1"));
    expect(screen.queryByTestId("table-new-table")).not.toBeInTheDocument();
    fireEvent.keyDown(document.body, { key: "Escape" });
    openNewMenu();
    await screen.findByTestId("database-new-menu-sql");
    expect(screen.queryByTestId("database-new-menu-table")).not.toBeInTheDocument();
  }

  it("驱动没声明建表", async () => {
    provideDrivers([fakeDriverWith(["create_database"])]);
    setup("fakedb");
    await expectNoCreateEntries();
  });

  it("资产开启了只读连接", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    setup("mysql", { readOnly: true });
    await expectNoCreateEntries();
  });
});
