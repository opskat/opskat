import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { notifySuccess } from "@/lib/notify";
import { asset_entity, dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import {
  ExecuteSchemaStatements,
  GetAlterTableModel,
  GetAlterTableStatements,
  GetCreateTableStatements,
  GetTableMetadata,
  ListDatabaseObjects,
  ListTables,
} from "../../../../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, fakeAlterDriver, provideDrivers } from "@/__tests__/dbDriverFixtures";
import { TableCreator } from "../TableCreator";
import { TableDesigner } from "../TableDesigner";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

// 新建表:同一个设计器从预置的 id 列开始,预览与执行的是驱动按草稿写出的建表语句;建好后标签转为这张表的
// 设计标签,失败时草稿留下;有改动的新建表标签关闭前拦下来问。

const TAB_ID = "query-1";
const db = () => useQueryStore.getState().dbStates[TAB_ID];
const createTab = () => db().innerTabs.find((t) => t.type === "designer" && t.mode === "create");

function setup(driver: string, opts: { tables?: Record<string, string[]>; databases?: string[] } = {}) {
  useTabStore.setState({
    tabs: [
      {
        id: TAB_ID,
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 1, assetName: "db", assetIcon: "", assetType: "database", driver },
      },
    ],
    activeTabId: TAB_ID,
    unsavedTabIds: [],
    pendingCloseTabId: null,
  });
  useAssetStore.setState({
    assets: [new asset_entity.Asset({ ID: 1, Name: "db", Type: "database", Config: JSON.stringify({ driver }) })],
  });
  useQueryStore.setState({
    dbStates: {
      [TAB_ID]: {
        databases: opts.databases ?? ["appdb", "other"],
        tables: opts.tables ?? { appdb: ["orders"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [],
        activeInnerTabId: null,
        error: null,
      },
    },
  });
}

/** 与 DatabasePanel 一样按 store 里的标签渲染(以标签 id 为 key,新建表转为设计标签后随之重新挂载)。 */
async function renderFromStore() {
  const FromStore = () => {
    const tab = useQueryStore((s) => s.dbStates[TAB_ID]?.innerTabs.find((t) => t.type === "designer"));
    if (!tab || tab.type !== "designer") return null;
    if (tab.mode === "create") {
      return (
        <TableCreator key={tab.id} tabId={TAB_ID} innerTabId={tab.id} database={tab.database} schema={tab.schema} />
      );
    }
    return (
      <TableDesigner
        key={tab.id}
        tabId={TAB_ID}
        innerTabId={tab.id}
        database={tab.database}
        table={tab.table}
        mode={tab.mode}
      />
    );
  };
  await act(async () => {
    render(
      <TooltipProvider>
        <FromStore />
      </TooltipProvider>
    );
  });
}

async function openCreate(driver: string, opts: Parameters<typeof setup>[1] & { schema?: string } = {}) {
  setup(driver, opts);
  act(() => useQueryStore.getState().openCreateTableTab(TAB_ID, "appdb", opts.schema));
  await renderFromStore();
  await screen.findByDisplayValue("id");
}

const createButton = () => screen.getByRole("button", { name: /query\.designer\.create\.submit/ });
const tableNameInput = () => screen.getByRole("textbox", { name: "query.designer.tableName" });

function lastCreateRequest() {
  const calls = vi.mocked(GetCreateTableStatements).mock.calls;
  const call = calls[calls.length - 1];
  return { database: call[1], request: JSON.parse(JSON.stringify(call[2])) };
}

function succeedAll() {
  vi.mocked(ExecuteSchemaStatements).mockImplementation(async (_a, _d, statements) =>
    query_svc.SchemaBatchResult.createFrom({
      atomic: true,
      rolledBack: false,
      statements: statements.map((statement) => ({ statement, status: "executed" })),
    })
  );
}

async function confirmCreate() {
  await waitFor(() => expect(createButton()).toBeEnabled());
  fireEvent.click(createButton());
  const dialog = await screen.findByRole("alertdialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "query.designer.save.run" }));
}

beforeEach(() => {
  vi.mocked(GetCreateTableStatements).mockReset();
  vi.mocked(GetCreateTableStatements).mockImplementation(async (_a, database, req) => [
    `CREATE TABLE ${database}.${req.name}`,
  ]);
  vi.mocked(ExecuteSchemaStatements).mockReset();
  vi.mocked(GetAlterTableModel).mockReset();
  vi.mocked(GetAlterTableStatements).mockReset();
  vi.mocked(GetAlterTableStatements).mockResolvedValue(
    dbdriver.AlterTableStatements.createFrom({ statements: [], table: "users" })
  );
  vi.mocked(GetTableMetadata).mockReset();
  vi.mocked(GetTableMetadata).mockResolvedValue(
    new query_svc.TableMetadata({ columns: [], indexes: [], foreignKeys: [] })
  );
  vi.mocked(ListTables).mockReset();
  vi.mocked(ListDatabaseObjects).mockReset();
  vi.mocked(ListDatabaseObjects).mockResolvedValue(new query_svc.DatabaseObjects({}));
  vi.mocked(notifySuccess).mockReset();
  provideDrivers(BUILTIN_DRIVERS);
});

describe("新建表的设计器", () => {
  it("预置自增主键 id;工具栏是目标库、表名与「创建」;没有外键子页;表名为空时不能创建", async () => {
    await openCreate("mysql");

    expect(screen.getByTestId("designer-create-database")).toHaveTextContent("appdb");
    expect(tableNameInput()).toHaveValue("");
    expect(screen.getByDisplayValue("BIGINT")).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: /query\.designer\.tab\.foreignKeys/ })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /query\.designer\.tab\.indexes/ })).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
    expect(GetCreateTableStatements).not.toHaveBeenCalled();
  });

  it("按草稿请驱动写建表语句:预置列与主键索引、写法与自增按声明发送", async () => {
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });

    await waitFor(() => expect(createButton()).toBeEnabled());
    expect(lastCreateRequest()).toEqual({
      database: "appdb",
      request: {
        name: "users",
        columns: [
          { name: "id", type: "BIGINT", nullable: false, defaultValue: "", defaultKind: "none", autoIncrement: true },
        ],
        indexes: [{ name: "idx_users_id", type: "primary", columns: [{ name: "id" }] }],
      },
    });
  });

  it("驱动写不出语句:SQL 预览显示驱动的错误,「创建」不可用", async () => {
    vi.mocked(GetCreateTableStatements).mockRejectedValue("unsupported: column type");
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });

    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.sql/ }));
    expect(await screen.findByText(/unsupported: column type/)).toBeInTheDocument();
    expect(createButton()).toBeDisabled();
  });

  it("自增只能在新表上设的驱动(SQLite):新建表的列可勾自增,主键类型可选", async () => {
    await openCreate("sqlite");
    fireEvent.click(screen.getByDisplayValue("id"));
    expect(screen.getByRole("checkbox", { name: "query.designer.autoIncrement" })).toBeEnabled();
    expect(screen.queryByText("query.designer.autoIncrementNewTablesOnly")).not.toBeInTheDocument();
  });

  it("没声明索引编辑的驱动:请求里没有索引,列上不标主键", async () => {
    provideDrivers([
      fakeAlterDriver({ capabilities: ["create_table", "alter_table"], columnTypes: undefined, id: "fakedb" }),
    ]);
    await openCreate("fakedb");
    fireEvent.change(tableNameInput(), { target: { value: "t" } });
    fireEvent.change(screen.getByRole("textbox", { name: "query.designer.column.type" }), {
      target: { value: "NUMBER" },
    });
    await waitFor(() => expect(createButton()).toBeEnabled());
    expect(lastCreateRequest().request).toEqual({
      name: "t",
      columns: [{ name: "id", type: "NUMBER", nullable: false, defaultValue: "" }],
    });
  });

  it("改表能写注释、但没声明建表注释的驱动(如已发布的 Oracle 扩展):没有注释输入,请求不带注释", async () => {
    provideDrivers([
      fakeAlterDriver({
        capabilities: ["create_table", "alter_table"],
        alterTableAttributes: ["table_comment", "column_comment"],
        columnTypes: undefined,
        id: "fakedb",
      }),
    ]);
    await openCreate("fakedb");
    expect(screen.queryByRole("textbox", { name: "query.designer.tableComment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "query.designer.column.comment" })).not.toBeInTheDocument();
  });

  it("声明了建表注释的驱动:有表注释与列注释输入,注释随请求发送", async () => {
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    fireEvent.change(screen.getByRole("textbox", { name: "query.designer.tableComment" }), {
      target: { value: "用户" },
    });
    expect(screen.getByRole("columnheader", { name: "query.designer.column.comment" })).toBeInTheDocument();
    await waitFor(() => expect(lastCreateRequest().request.comment).toBe("用户"));
  });

  it("换目标库:标签与请求跟着换", async () => {
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    fireEvent.pointerDown(screen.getByTestId("designer-create-database"), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitemradio", { name: "other" }));

    await waitFor(() => expect(createTab()).toMatchObject({ database: "other" }));
    await waitFor(() => expect(lastCreateRequest().database).toBe("other"));
    // 草稿不丢。
    expect(tableNameInput()).toHaveValue("users");
  });

  it("有 schema 的驱动:选到 schema,请求的表名带上它,建好后设计标签是 schema.表", async () => {
    await openCreate("postgresql", { tables: { appdb: ["public.orders", "sales.items"] }, schema: "sales" });
    expect(screen.getByTestId("designer-create-schema")).toHaveTextContent("sales");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    await waitFor(() => expect(lastCreateRequest().request.name).toBe("sales.users"));

    succeedAll();
    vi.mocked(ListTables).mockResolvedValue(["public.orders", "sales.items", "sales.users"]);
    await confirmCreate();
    await waitFor(() => expect(db().innerTabs.map((t) => t.id)).toEqual(["designer:appdb.sales.users"]));
  });

  it("默认 schema:请求的表名不带 schema,建好后按树上列出的 schema.表 找到这张表", async () => {
    await openCreate("postgresql", { tables: { appdb: ["public.orders"] } });
    expect(screen.getByTestId("designer-create-schema")).toHaveTextContent("query.designer.create.defaultSchema");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    await waitFor(() => expect(lastCreateRequest().request.name).toBe("users"));

    succeedAll();
    vi.mocked(ListTables).mockResolvedValue(["public.orders", "public.users"]);
    await confirmCreate();
    await waitFor(() => expect(db().innerTabs.map((t) => t.id)).toEqual(["designer:appdb.public.users"]));
  });

  it("创建成功:成功提示、树刷新、标签转为这张表的设计标签并读取它", async () => {
    vi.mocked(GetAlterTableModel).mockResolvedValue(
      new dbdriver.AlterTableModel({
        name: "users",
        comment: "",
        columns: [{ name: "id", type: "bigint", nullable: false, defaultValue: "", defaultKind: "none", comment: "" }],
        indexes: [],
      })
    );
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    succeedAll();
    vi.mocked(ListTables).mockResolvedValue(["orders", "users"]);
    await confirmCreate();

    expect(ExecuteSchemaStatements).toHaveBeenCalledWith(1, "appdb", ["CREATE TABLE appdb.users"]);
    await waitFor(() => expect(db().innerTabs.map((t) => t.id)).toEqual(["designer:appdb.users"]));
    expect(notifySuccess).toHaveBeenCalledWith("query.designer.create.done");
    expect(ListTables).toHaveBeenCalledWith(1, "appdb");
    expect(db().tables.appdb).toEqual(["orders", "users"]);
    expect(db().innerTabs[0]).toMatchObject({ mode: "design", table: "users" });
    await waitFor(() => expect(GetAlterTableModel).toHaveBeenCalledWith(1, "appdb", "users"));
  });

  it("整批回滚:标签不变,草稿留下", async () => {
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    vi.mocked(ExecuteSchemaStatements).mockResolvedValue(
      query_svc.SchemaBatchResult.createFrom({
        atomic: true,
        rolledBack: true,
        statements: [{ statement: "CREATE TABLE appdb.users", status: "failed", error: "boom" }],
      })
    );
    await confirmCreate();

    expect(await screen.findByText("query.designer.save.rolledBackTitle")).toBeInTheDocument();
    expect(createTab()).toBeDefined();
    expect(notifySuccess).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.save.back" }));
    expect(tableNameInput()).toHaveValue("users");
  });

  it("逐条执行停在中途、表已建出:标签转为设计标签,没执行的留在草稿里,结果视图随之交过去", async () => {
    vi.mocked(GetAlterTableModel).mockResolvedValue(
      new dbdriver.AlterTableModel({
        name: "users",
        comment: "",
        columns: [{ name: "id", type: "BIGINT", nullable: false, defaultValue: "", defaultKind: "none", comment: "" }],
        indexes: [],
      })
    );
    await openCreate("mysql");
    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    vi.mocked(GetCreateTableStatements).mockResolvedValue(["CREATE TABLE users", "CREATE INDEX pk"]);
    vi.mocked(ExecuteSchemaStatements).mockResolvedValue(
      query_svc.SchemaBatchResult.createFrom({
        atomic: false,
        rolledBack: false,
        statements: [
          { statement: "CREATE TABLE users", status: "executed" },
          { statement: "CREATE INDEX pk", status: "failed", error: "boom" },
        ],
      })
    );
    vi.mocked(ListTables).mockResolvedValue(["orders", "users"]);
    await confirmCreate();

    await waitFor(() => expect(db().innerTabs.map((t) => t.id)).toEqual(["designer:appdb.users"]));
    expect(await screen.findByText("query.designer.save.partialTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "query.designer.save.back" }));
    // 主键索引没建出来,留在草稿里作为新索引。
    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.indexes/ }));
    expect(await screen.findByText("idx_users_id")).toBeInTheDocument();
  });

  it("有改动才算未保存;关闭时拦下来问,放弃并关闭关掉", async () => {
    await openCreate("mysql");
    const id = createTab()!.id;
    expect(db().dirtyInnerTabIds ?? []).not.toContain(id);

    fireEvent.change(tableNameInput(), { target: { value: "users" } });
    await waitFor(() => expect(db().dirtyInnerTabIds).toContain(id));
    act(() => useQueryStore.getState().closeInnerTab(TAB_ID, id));
    const guard = await screen.findByRole("alertdialog");
    expect(within(guard).getByText("query.designer.guard.createTitle")).toBeInTheDocument();
    fireEvent.click(within(guard).getByRole("button", { name: "query.unsavedEditsDiscardClose" }));
    await waitFor(() => expect(db().innerTabs).toEqual([]));
  });
});
