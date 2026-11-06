import { act, fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";
import { TooltipProvider } from "@opskat/ui";
import { TableDesigner } from "../TableDesigner";
import { useQueryStore, type InnerTab } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity, dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import { GetAlterTableModel, GetAlterTableStatements, GetTableMetadata } from "../../../../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, provideDrivers } from "@/__tests__/dbDriverFixtures";

// 设计表测试共用的夹具:资产与查询标签、MySQL 与旧扩展的改表模型、像驱动一样回答的语句生成,以及按
// 查询 store 里的设计器标签渲染设计器(与 DatabasePanel 一样以标签 id 为 key,改名后随之重新挂载)。

export const TAB_ID = "query-1";
export const INNER_ID = "designer:appdb.orders";

export function setup(driver: string, opts: { readOnly?: boolean; otherTabs?: InnerTab[] } = {}) {
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
      [TAB_ID]: {
        databases: ["appdb"],
        tables: { appdb: ["orders"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [
          { id: INNER_ID, type: "designer", mode: "design", database: "appdb", table: "orders" },
          ...(opts.otherTabs ?? []),
        ],
        activeInnerTabId: INNER_ID,
        error: null,
      },
    },
  });
}

/** MySQL 驱动读出的改表模型(带写法与自增现状)。 */
export function mysqlModel(): dbdriver.AlterTableModel {
  return new dbdriver.AlterTableModel({
    name: "orders",
    comment: "销售订单",
    columns: [
      {
        name: "id",
        type: "bigint unsigned",
        nullable: false,
        defaultValue: "",
        defaultKind: "none",
        comment: "订单 ID",
        autoIncrement: true,
      },
      {
        name: "status",
        type: "varchar(16)",
        nullable: false,
        defaultValue: "pending",
        defaultKind: "value",
        comment: "",
      },
      { name: "note", type: "varchar(255)", nullable: true, defaultValue: "", defaultKind: "null", comment: "备注" },
    ],
    indexes: [
      { name: "PRIMARY", type: "primary", columns: [{ name: "id" }] },
      { name: "idx_note", type: "normal", columns: [{ name: "note" }] },
    ],
  });
}

/** 没声明写法的驱动读出的改表模型:默认值是原文。 */
export function legacyModel(): dbdriver.AlterTableModel {
  return new dbdriver.AlterTableModel({
    name: "ORDERS",
    comment: "",
    columns: [
      { name: "ID", type: "NUMBER(19)", nullable: false, defaultValue: "", comment: "" },
      { name: "STATUS", type: "VARCHAR2(16)", nullable: true, defaultValue: "'pending'", comment: "" },
    ],
  });
}

export const METADATA = new query_svc.TableMetadata({ columns: [], indexes: [], foreignKeys: [] });

/**
 * 像驱动一样回答 GetAlterTableStatements:请求与读出的模型一致(每列原名、类型、可空、写法与值、注释
 * 都相同,没有删除,表名注释不变)时没有语句;否则每处改动一条,一眼能与真实方言区分。改了表名时
 * 改表后的表名是新名字。model 取调用时的值,测试换掉它就是数据库里的表变了。
 */
export function mockDriverStatements(model: () => dbdriver.AlterTableModel) {
  vi.mocked(GetAlterTableStatements).mockImplementation(async (_a, _d, table, req) => {
    const current = model();
    const out: string[] = [];
    if (req.name !== current.name) out.push(`RENAME ${req.name}`);
    if (req.comment !== current.comment) out.push(`COMMENT ${req.comment}`);
    for (const c of req.columns) {
      const o = current.columns.find((m) => m.name === c.originalName);
      const same =
        o &&
        o.name === c.name &&
        o.type === c.type &&
        o.nullable === c.nullable &&
        o.defaultValue === c.defaultValue &&
        o.defaultKind === c.defaultKind &&
        o.comment === c.comment &&
        !c.autoIncrement;
      if (!same) out.push(`${o ? "MODIFY" : "ADD"} ${JSON.stringify(c)}`);
    }
    for (const d of req.drops) out.push(`DROP ${d}`);
    // 索引:交回的已有索引与现状(列按改名之后的名字)相同时没有语句,改了的一条,新建、删除各一条。
    const renamed = new Map(req.columns.filter((c) => c.originalName).map((c) => [c.originalName, c.name]));
    const shape = (i: dbdriver.IndexDefinition, rename: (name: string) => string) =>
      JSON.stringify([
        i.name,
        i.type,
        i.columns.map((c) => [c.expression ? c.name : rename(c.name), !!c.descending, c.prefixLength ?? 0]),
        i.options ?? {},
        i.comment ?? "",
      ]);
    for (const index of req.indexes ?? []) {
      const o = current.indexes?.find((m) => m.name === index.originalName);
      if (!o) out.push(`CREATE INDEX ${JSON.stringify(index)}`);
      else if (shape(o, (name) => renamed.get(name) ?? name) !== shape(index, (name) => name)) {
        out.push(`REBUILD INDEX ${JSON.stringify(index)}`);
      }
    }
    for (const d of req.indexDrops ?? []) out.push(`DROP INDEX ${d}`);
    return dbdriver.AlterTableStatements.createFrom({
      statements: out,
      table: req.name !== current.name ? req.name : table,
    });
  });
}

/** 按 store 里的设计器标签渲染设计器;标签关掉后不渲染。 */
export async function renderDesigner() {
  const DesignerFromStore = () => {
    const tab = useQueryStore((s) => s.dbStates[TAB_ID]?.innerTabs.find((t) => t.type === "designer"));
    if (!tab || tab.type !== "designer" || tab.mode === "create") return null;
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
        <DesignerFromStore />
      </TooltipProvider>
    );
  });
}

/** 打开 MySQL 表 orders 的设计器;返回读出的模型(换掉 holder.model 即数据库里的表变了)。 */
export async function openMysql(holder: { model: dbdriver.AlterTableModel } = { model: mysqlModel() }) {
  provideDrivers(BUILTIN_DRIVERS);
  setup("mysql");
  vi.mocked(GetAlterTableModel).mockImplementation(async () => holder.model);
  vi.mocked(GetTableMetadata).mockResolvedValue(METADATA);
  mockDriverStatements(() => holder.model);
  await renderDesigner();
  await screen.findByDisplayValue("status");
  return holder.model;
}

export async function openWith(descriptor: dbdriver.Descriptor, model = legacyModel()) {
  provideDrivers([descriptor]);
  setup(descriptor.id);
  vi.mocked(GetAlterTableModel).mockResolvedValue(model);
  vi.mocked(GetTableMetadata).mockResolvedValue(METADATA);
  mockDriverStatements(() => model);
  await renderDesigner();
  await screen.findByDisplayValue(model.columns[1].name);
}

export function rowOf(name: string): HTMLElement {
  return screen.getByDisplayValue(name).closest('[role="row"]') as HTMLElement;
}

export function lastRow(): HTMLElement {
  const rows = screen.getAllByRole("row");
  return rows[rows.length - 1];
}

export function addField() {
  fireEvent.click(screen.getByRole("button", { name: "query.designer.addField" }));
  return lastRow();
}

export function openSqlTab() {
  fireEvent.mouseDown(screen.getByRole("tab", { name: /query\.designer\.tab\.sql/ }));
}

/** 最近一次发给驱动的改表请求(按 JSON,即真正传给后端的样子)。 */
export function lastRequest() {
  const calls = vi.mocked(GetAlterTableStatements).mock.calls;
  return JSON.parse(JSON.stringify(calls[calls.length - 1][3]));
}

export const saveButton = () => screen.getByRole("button", { name: /action\.save/ });
