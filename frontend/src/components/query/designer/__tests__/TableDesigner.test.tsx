import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { TableDesigner } from "../TableDesigner";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity, dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import {
  GetAlterTableModel,
  GetTableDDL,
  GetTableMetadata,
  ListDatabaseDrivers,
} from "../../../../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "@/__tests__/dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

const INNER_ID = "designer:appdb.orders";

function setup(driver: string, opts: { readOnly?: boolean; active?: boolean } = {}) {
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
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["orders"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [
          { id: "table:appdb.other", type: "table", database: "appdb", table: "other" },
          { id: INNER_ID, type: "designer", mode: "view", database: "appdb", table: "orders" },
        ],
        activeInnerTabId: opts.active === false ? "table:appdb.other" : INNER_ID,
        error: null,
      },
    },
  });
}

function metadata(patch: Partial<query_svc.TableMetadata> = {}) {
  return new query_svc.TableMetadata({
    columns: [
      { name: "id", type: "bigint", nullable: false, primaryKey: true, comment: "", default: null },
      { name: "status", type: "varchar(16)", nullable: true, primaryKey: false, comment: "", default: "'pending'" },
    ],
    indexes: [
      { name: "PRIMARY", columns: ["id"], unique: true },
      { name: "uq_status", columns: ["status"], unique: true },
    ],
    foreignKeys: [
      {
        name: "fk_customer",
        column: "customer_id",
        referencedTable: "customers",
        referencedColumn: "id",
        onUpdate: "CASCADE",
        onDelete: "RESTRICT",
      },
    ],
    ...patch,
  });
}

async function renderDesigner() {
  await act(async () => {
    render(
      <TooltipProvider>
        <TableDesigner tabId="query-1" innerTabId={INNER_ID} database="appdb" table="orders" mode="view" />
      </TooltipProvider>
    );
  });
}

beforeEach(() => {
  vi.mocked(GetTableMetadata).mockReset();
  vi.mocked(GetAlterTableModel).mockReset();
  vi.mocked(GetTableDDL).mockReset();
});

describe("TableDesigner 只读查看", () => {
  it("只读连接:按表元数据读,工具栏表名是文本并标明原因,没有保存", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    setup("mysql", { readOnly: true });
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();

    expect(await screen.findByText("status")).toBeInTheDocument();
    expect(GetTableMetadata).toHaveBeenCalledWith(1, "appdb", "orders");
    expect(GetAlterTableModel).not.toHaveBeenCalled();
    expect(screen.getByTestId("designer-table-name")).toHaveTextContent("orders");
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.getByTestId("designer-readonly")).toHaveTextContent("query.designer.readOnly.readOnlyConnection");
    expect(screen.queryByRole("button", { name: /action\.save/ })).not.toBeInTheDocument();
    expect(screen.getByTestId("designer-status")).toHaveTextContent("MySQL");
    expect(screen.getByTestId("designer-status")).toHaveTextContent("appdb.orders");
  });

  it("驱动不能改表:标明驱动不支持;没声明查看 DDL 时没有建表语句子页", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();

    expect(await screen.findByText("status")).toBeInTheDocument();
    expect(screen.getByTestId("designer-readonly")).toHaveTextContent("query.designer.readOnly.driverCannotAlter");
    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
      expect.stringContaining("query.designer.tab.fields"),
      expect.stringContaining("query.designer.tab.indexes"),
      expect.stringContaining("query.designer.tab.foreignKeys"),
    ]);
  });

  it("字段:主键、类型、可空、默认值原文", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();

    const row = (await screen.findByText("status")).closest('[role="row"]') as HTMLElement;
    expect(within(row).getByText("varchar(16)")).toBeInTheDocument();
    expect(within(row).getByText("'pending'")).toBeInTheDocument();
    expect(within(row).getByText("query.designer.yes")).toBeInTheDocument();
    const idRow = screen.getByText("id").closest('[role="row"]') as HTMLElement;
    expect(within(idRow).getByLabelText("query.designer.column.primaryKey")).toBeInTheDocument();
    expect(within(row).queryByLabelText("query.designer.column.primaryKey")).not.toBeInTheDocument();
  });

  it("驱动不支持列注释、表里也没有注释时不显示注释列;有注释或声明了列注释时显示", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();
    await screen.findByText("status");
    expect(screen.queryByText("query.designer.column.comment")).not.toBeInTheDocument();
  });

  it("表里有列注释时显示注释列", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    const withComment = metadata();
    withComment.columns[0].comment = "订单 ID";
    vi.mocked(GetTableMetadata).mockResolvedValue(withComment);
    await renderDesigner();
    expect(await screen.findByText("订单 ID")).toBeInTheDocument();
    expect(screen.getByText("query.designer.column.comment")).toBeInTheDocument();
  });

  it("驱动声明了列注释时即使都为空也显示注释列", async () => {
    provideDrivers([new dbdriver.Descriptor({ ...fakeDriverWith([]), alterTableAttributes: ["column_comment"] })]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();
    await screen.findByText("status");
    expect(screen.getByText("query.designer.column.comment")).toBeInTheDocument();
  });

  it("索引子页:类型按宿主文案显示主键 / 唯一", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();
    await screen.findByText("status");

    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.indexes/ }));
    const row = (await screen.findByText("uq_status")).closest('[role="row"]') as HTMLElement;
    expect(within(row).getByText("query.designer.indexType.unique")).toBeInTheDocument();
    const pk = screen.getByText("PRIMARY").closest('[role="row"]') as HTMLElement;
    expect(within(pk).getByText("query.designer.indexType.primary")).toBeInTheDocument();
  });

  it("外键子页:顶部注明只读,列出名称、列、引用与动作", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();
    await screen.findByText("status");

    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.foreignKeys/ }));
    expect(await screen.findByText("query.designer.foreignKeysReadOnly")).toBeInTheDocument();
    const row = screen.getByText("fk_customer").closest('[role="row"]') as HTMLElement;
    expect(row).toHaveTextContent("customer_id");
    expect(row).toHaveTextContent("customers (id)");
    expect(row).toHaveTextContent("CASCADE");
    expect(row).toHaveTextContent("RESTRICT");
  });

  it("建表语句子页显示驱动给出的 DDL", async () => {
    provideDrivers(BUILTIN_DRIVERS);
    setup("mysql", { readOnly: true });
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    vi.mocked(GetTableDDL).mockResolvedValue("CREATE TABLE orders (id bigint)");
    await renderDesigner();
    await screen.findByText("status");

    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.ddl/ }));
    expect(await screen.findByTestId("code-editor")).toHaveTextContent("CREATE TABLE orders (id bigint)");
    expect(GetTableDDL).toHaveBeenCalledWith(1, "appdb", "orders");
  });

  it("重新读取表结构", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockResolvedValueOnce(metadata());
    await renderDesigner();
    await screen.findByText("status");

    vi.mocked(GetTableMetadata).mockResolvedValueOnce(
      metadata({
        columns: [{ name: "renamed", type: "int", nullable: false, primaryKey: false, comment: "" }],
      })
    );
    fireEvent.click(screen.getByRole("button", { name: "query.designer.reload" }));
    expect(await screen.findByText("renamed")).toBeInTheDocument();
    expect(GetTableMetadata).toHaveBeenCalledTimes(2);
  });
});

describe("TableDesigner 加载与失败", () => {
  it("读取期间保留工具栏与子页,表格区是骨架", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockReturnValue(new Promise(() => {}));
    await renderDesigner();

    expect(screen.getByTestId("designer-skeleton")).toBeInTheDocument();
    expect(screen.getByTestId("designer-table-name")).toHaveTextContent("orders");
    expect(screen.getByRole("tab", { name: /query\.designer\.tab\.fields/ })).toBeInTheDocument();
  });

  it("驱动描述还没到时也是骨架,不发请求", async () => {
    provideDrivers([]);
    vi.mocked(ListDatabaseDrivers).mockReturnValue(new Promise(() => {}));
    setup("fakedb");
    await renderDesigner();

    expect(screen.getByTestId("designer-skeleton")).toBeInTheDocument();
    expect(GetTableMetadata).not.toHaveBeenCalled();
  });

  it("读取失败显示驱动原始错误,可重试", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockRejectedValueOnce("Error 1142 (42000): SELECT command denied");
    await renderDesigner();

    expect(await screen.findByText("Error 1142 (42000): SELECT command denied")).toBeInTheDocument();
    expect(screen.queryByTestId("designer-skeleton")).not.toBeInTheDocument();

    vi.mocked(GetTableMetadata).mockResolvedValueOnce(metadata());
    fireEvent.click(screen.getByRole("button", { name: "action.retry" }));
    expect(await screen.findByText("status")).toBeInTheDocument();
    expect(screen.queryByText("Error 1142 (42000): SELECT command denied")).not.toBeInTheDocument();
  });

  it("读取失败时可以关闭标签", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb");
    vi.mocked(GetTableMetadata).mockRejectedValueOnce("boom");
    await renderDesigner();

    fireEvent.click(await screen.findByRole("button", { name: "query.designer.closeTab" }));
    expect(useQueryStore.getState().dbStates["query-1"].innerTabs.map((t) => t.id)).toEqual(["table:appdb.other"]);
  });

  it("标签不在前台时不读;切到前台才读(恢复后的标签也是这样重新读取)", async () => {
    provideDrivers([fakeDriverWith([])]);
    setup("fakedb", { active: false });
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();
    await new Promise((r) => setTimeout(r, 10));
    expect(GetTableMetadata).not.toHaveBeenCalled();

    act(() => useQueryStore.getState().setActiveInnerTab("query-1", INNER_ID));
    await waitFor(() => expect(GetTableMetadata).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("status")).toBeInTheDocument();
  });
});

describe("TableDesigner 读改表模型", () => {
  it("能改表时按改表模型读,表注释显示为文本", async () => {
    provideDrivers([fakeDriverWith(["alter_table"])]);
    setup("fakedb");
    vi.mocked(GetAlterTableModel).mockResolvedValue(
      new dbdriver.AlterTableModel({
        name: "orders",
        comment: "销售订单",
        columns: [{ name: "status", type: "varchar(16)", nullable: true, defaultValue: "", comment: "" }],
      })
    );
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();

    expect(await screen.findByText("销售订单")).toBeInTheDocument();
    expect(GetAlterTableModel).toHaveBeenCalledWith(1, "appdb", "orders");
  });

  it("改表模型不带索引(驱动没声明索引编辑)时,索引取自表元数据并说明不能在设计器里改", async () => {
    provideDrivers([fakeDriverWith(["alter_table"])]);
    setup("fakedb");
    vi.mocked(GetAlterTableModel).mockResolvedValue(
      new dbdriver.AlterTableModel({
        name: "orders",
        comment: "",
        columns: [{ name: "status", type: "varchar(16)", nullable: true, defaultValue: "", comment: "" }],
      })
    );
    vi.mocked(GetTableMetadata).mockResolvedValue(metadata());
    await renderDesigner();
    await screen.findByText("status");

    fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.indexes/ }));
    expect(await screen.findByText("uq_status")).toBeInTheDocument();
    expect(screen.getByText("query.designer.indexesNotEditable")).toBeInTheDocument();
  });
});
