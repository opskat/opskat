import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { DatabaseTree } from "../components/query/DatabaseTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { ListDatabaseDrivers, ListDatabaseObjects, ListDatabases, ListTables } from "../../wailsjs/go/query/Query";
import { query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

// 带插值参数的文案要能看出参数(全局 mock 只回键名)。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key} ${Object.values(opts).join(" ")}` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }), initReactI18next: { type: "3rdParty", init: vi.fn() } };
});

const ALL_OBJECT_GROUPS = ["query.objViews", "query.objProcedures", "query.objFunctions", "query.objTriggers"];
const ALL_TABLE_MENU = ["query.openTable", "query.alterTable", "query.truncateTable", "query.dropTable"];

function openTab(driver: string) {
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
          driver,
          defaultDatabase: "appdb",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["public.users"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: { appdb: ["public"] },
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

type Group = "views" | "procedures" | "functions" | "triggers";

/** 驱动返回的对象:指定的几类各有一个。 */
function objectsOf(...groups: Group[]) {
  const item = (name: string) => [new query_svc.ObjectMeta({ name })];
  return new query_svc.DatabaseObjects({
    views: groups.includes("views") ? item("v1") : [],
    procedures: groups.includes("procedures") ? item("p1") : [],
    functions: groups.includes("functions") ? item("f1") : [],
    triggers: groups.includes("triggers") ? item("t1") : [],
  });
}

const EVERY_GROUP: Group[] = ["views", "procedures", "functions", "triggers"];

async function renderTree(driver: string, objects = objectsOf(...EVERY_GROUP)) {
  vi.mocked(ListDatabaseObjects).mockResolvedValue(objects);
  openTab(driver);
  render(<DatabaseTree tabId="query-1" />);
  await screen.findByText(/^query\.objTables/);
}

const hasToolbarCreateDatabase = () => screen.queryByLabelText("query.createDatabase") !== null;
const hasSchemaGroup = () => screen.queryByText("public") !== null;

function tableMenuLabels(): string[] {
  fireEvent.contextMenu(screen.getByText(/users$/));
  return ALL_TABLE_MENU.filter((label) => screen.queryByText(label) !== null);
}

function databaseMenuHasAddTable(): boolean {
  fireEvent.contextMenu(screen.getByText("appdb"));
  return screen.queryByText("query.addTable") !== null;
}

async function objectGroupsShown(): Promise<string[]> {
  // 对象列表异步到达;让它落地后再读界面。
  await new Promise((r) => setTimeout(r, 10));
  return ALL_OBJECT_GROUPS.filter((label) => screen.queryByText(new RegExp(`^${label}`)) !== null);
}

beforeEach(() => {
  vi.mocked(ListDatabaseObjects).mockReset();
  vi.mocked(ListDatabases).mockReset();
  vi.mocked(ListTables).mockReset();
});

describe("DatabaseTree 内置驱动的入口与改动前一致", () => {
  const expected: Record<
    string,
    { createDatabase: boolean; schemaGroup: boolean; objects: string[]; backend: Group[] }
  > = {
    mysql: { createDatabase: true, schemaGroup: false, objects: ALL_OBJECT_GROUPS, backend: EVERY_GROUP },
    postgresql: { createDatabase: true, schemaGroup: true, objects: ALL_OBJECT_GROUPS, backend: EVERY_GROUP },
    mssql: { createDatabase: true, schemaGroup: true, objects: ALL_OBJECT_GROUPS, backend: EVERY_GROUP },
    // 后端对 SQLite 只返回视图与触发器。
    sqlite: {
      createDatabase: false,
      schemaGroup: false,
      objects: ["query.objViews", "query.objTriggers"],
      backend: ["views", "triggers"],
    },
  };

  for (const [driver, want] of Object.entries(expected)) {
    it(driver, async () => {
      provideDrivers(BUILTIN_DRIVERS);
      await renderTree(driver, objectsOf(...want.backend));

      expect(hasToolbarCreateDatabase()).toBe(want.createDatabase);
      expect(hasSchemaGroup()).toBe(want.schemaGroup);
      expect(await objectGroupsShown()).toEqual(want.objects);
      expect(tableMenuLabels()).toEqual(ALL_TABLE_MENU);
      cleanup();
      await renderTree(driver, objectsOf(...want.backend));
      expect(databaseMenuHasAddTable()).toBe(true);
    });
  }
});

describe("DatabaseTree 的入口跟随一个前端一无所知的驱动所声明的功能", () => {
  it("什么都不声明:没有任何可选入口,表也不分组", async () => {
    provideDrivers([fakeDriverWith([])]);
    await renderTree("fakedb");

    expect(hasToolbarCreateDatabase()).toBe(false);
    expect(hasSchemaGroup()).toBe(false);
    expect(screen.getByText("public.users")).toBeInTheDocument();
    expect(await objectGroupsShown()).toEqual([]);
    expect(tableMenuLabels()).toEqual(["query.openTable"]);
    cleanup();
    await renderTree("fakedb");
    expect(databaseMenuHasAddTable()).toBe(false);
  });

  it("声明 schemas:表按 schema 分组", async () => {
    provideDrivers([fakeDriverWith(["schemas"])]);
    await renderTree("fakedb");

    expect(hasSchemaGroup()).toBe(true);
    expect(screen.getByText("users")).toBeInTheDocument();
    expect(screen.queryByText("public.users")).not.toBeInTheDocument();
  });

  it("声明 create_database:出现「新建数据库」", async () => {
    provideDrivers([fakeDriverWith(["create_database"])]);
    await renderTree("fakedb");
    expect(hasToolbarCreateDatabase()).toBe(true);
  });

  it("声明 create_table:数据库右键菜单出现「新建表」", async () => {
    provideDrivers([fakeDriverWith(["create_table"])]);
    await renderTree("fakedb");
    expect(databaseMenuHasAddTable()).toBe(true);
  });

  for (const [cap, label] of [
    ["alter_table", "query.alterTable"],
    ["truncate_table", "query.truncateTable"],
    ["drop_table", "query.dropTable"],
  ]) {
    it(`声明 ${cap}:表的右键菜单只多出对应一项`, async () => {
      provideDrivers([fakeDriverWith([cap])]);
      await renderTree("fakedb");
      expect(tableMenuLabels()).toEqual(["query.openTable", label]);
    });
  }

  it("对象浏览按四类对象各自的声明显示", async () => {
    provideDrivers([fakeDriverWith(["procedures", "triggers"])]);
    await renderTree("fakedb");
    expect(await objectGroupsShown()).toEqual(["query.objProcedures", "query.objTriggers"]);
  });
});

describe("DatabaseTree 在资产的驱动没有注册时", () => {
  beforeEach(() => {
    vi.mocked(ListDatabases).mockResolvedValue(["appdb"]);
  });

  it("显示指明驱动名的「驱动不可用」,不向后端发任何请求", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    openTab("ghostdb");
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: [],
          tables: {},
          loadingTables: {},
          expandedDbs: [],
          expandedSchemas: {},
          loadingDbs: false,
          innerTabs: [],
          activeInnerTabId: null,
          error: null,
        },
      },
    });
    render(<DatabaseTree tabId="query-1" />);

    const notice = await screen.findByRole("alert");
    expect(notice).toHaveTextContent("query.driverUnavailable");
    expect(notice).toHaveTextContent("ghostdb");
    await new Promise((r) => setTimeout(r, 10));
    expect(ListDatabases).not.toHaveBeenCalled();
    expect(ListTables).not.toHaveBeenCalled();
    expect(hasToolbarCreateDatabase()).toBe(false);
  });

  it("描述还在加载时不闪出「驱动不可用」", async () => {
    let resolve!: (v: typeof BUILTIN_DRIVERS) => void;
    provideDrivers([]);
    vi.mocked(ListDatabaseDrivers).mockReturnValue(new Promise((r) => (resolve = r)));
    openTab("mysql");
    render(<DatabaseTree tabId="query-1" />);

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    resolve(BUILTIN_DRIVERS);
    await screen.findByText(/^query\.objTables/);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await waitFor(() => expect(hasToolbarCreateDatabase()).toBe(true));
  });
});
