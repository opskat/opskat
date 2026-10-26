import { vi } from "vitest";
import { dbdriver } from "../../wailsjs/go/models";
import {
  GetCreateDatabaseStatements,
  GetCreateTableStatements,
  GetDropTableStatements,
  GetGridEditStatements,
  GetImportStatements,
  GetTruncateTableStatements,
  ListDatabaseDrivers,
} from "../../wailsjs/go/query/Query";
import { refreshDatabaseDrivers } from "@/lib/dbDrivers";
import builtinDrivers from "./builtinDbDrivers.json";

type FormSpec = Record<string, string[]>;

function driver(d: {
  id: string;
  name: string;
  icon: string;
  order: number;
  defaultPort: number;
  form: FormSpec;
  capabilities?: string[];
}): dbdriver.Descriptor {
  return new dbdriver.Descriptor({
    id: d.id,
    name: d.name,
    icon: d.icon,
    order: d.order,
    aliases: [],
    defaultPort: d.defaultPort,
    form: Object.entries(d.form).map(([tab, fields]) => ({ tab, fields })),
    capabilities: d.capabilities ?? [],
  });
}

/**
 * 后端内置四个驱动的描述,供前端测试当作 Query.ListDatabaseDrivers 的返回值。
 * 内容就是注册表序列化出来的 JSON,由 Go 侧 internal/archtest 的
 * TestFrontendBuiltinDriverFixtureMatchesRegistry 核对:驱动描述变了而它没跟着改,那条测试先红。
 */
export const BUILTIN_DRIVERS: dbdriver.Descriptor[] = builtinDrivers.map((d) => new dbdriver.Descriptor(d));

/** 前端从未听说过的驱动:带用户名/库名、TLS 与 SSL 模式,不用口令认证。 */
export const FAKE_DRIVER: dbdriver.Descriptor = driver({
  id: "fakedb",
  name: "FakeDB",
  icon: "fake-icon",
  order: 5,
  defaultPort: 9042,
  form: {
    connection: ["host", "port", "username", "database"],
    tls: ["ssl_mode"],
    advanced: ["query_timeout_seconds"],
  },
});

/**
 * 扩展提供的驱动:除了内置表单字段,还声明了各类专属字段(标签与说明已由扩展的多语言解析好)。
 * 专属字段分布在连接 Tab(表单本来就有)和高级 / TLS Tab(表单没声明,由专属字段带出来)。
 */
export const EXT_DRIVER: dbdriver.Descriptor = new dbdriver.Descriptor({
  ...driver({
    id: "extdb",
    name: "ExtDB",
    icon: "database",
    order: 900,
    defaultPort: 7357,
    form: { connection: ["host", "port", "username", "password", "database"] },
  }),
  driverFields: [
    {
      key: "service_name",
      type: "text",
      tab: "connection",
      required: true,
      label: "Service name",
      description: "Listener service",
    },
    { key: "wallet_password", type: "password", tab: "connection", label: "Wallet password" },
    { key: "role", type: "enum", tab: "advanced", enum: ["normal", "sysdba"], default: "normal", label: "Role" },
    { key: "fetch_size", type: "number", tab: "advanced", default: 100, label: "Fetch size" },
    { key: "native_encryption", type: "switch", tab: "tls", label: "Native encryption" },
  ],
});

/** 一个只在测试里存在的驱动,功能集合由测试指定,用来证明界面入口跟随驱动的声明。 */
export function fakeDriverWith(capabilities: string[], id = FAKE_DRIVER.id): dbdriver.Descriptor {
  return new dbdriver.Descriptor({ ...FAKE_DRIVER, id, name: "FakeDB", capabilities });
}

/**
 * 让 Query.GetGridEditStatements 像驱动一样给出表格编辑的语句:每个新增 / 改动 / 删除的行各一条,
 * 内容是请求本身(值按 JSON 写),一眼能与任何真实方言区分,测试据此断言前端发出的意图与执行的语句。
 * 定位条件按主键取值,没有主键时是整行。
 */
export function mockGridEditStatements(): void {
  const json = (pairs: { column: string; value: unknown }[]) =>
    JSON.stringify(Object.fromEntries(pairs.map(({ column, value }) => [column, value])));
  vi.mocked(GetGridEditStatements).mockImplementation(async (_assetId, _database, table, req) => {
    const locate = (row: Record<string, unknown>) =>
      JSON.stringify(
        req.primaryKeys.length > 0 ? Object.fromEntries(req.primaryKeys.map((key) => [key, row[key]])) : row
      );
    return dbdriver.GridEditStatements.createFrom({
      inserts: req.inserts.map((insert) => `INSERT ${table} ${json(insert.values)}`),
      updates: req.updates.map((update) => `UPDATE ${table} SET ${json(update.set)} WHERE ${locate(update.row)}`),
      deletes: req.deletes.map((row) => `DELETE ${table} WHERE ${locate(row)}`),
    });
  });
}

/**
 * 让 Query.GetImportStatements 像驱动一样给出导入的语句:每行一条,内容是模式、表与这一行的值(文本、
 * null 或 0x 开头的二进制),复制模式前面多一条清空,一眼能与任何真实方言区分。
 */
export function mockImportStatements(): void {
  vi.mocked(GetImportStatements).mockImplementation(async (_assetId, _database, table, req) => {
    const rows = req.rows.map(
      (row) =>
        `IMPORT ${req.mode} ${table} ${JSON.stringify(row.map((v) => (v.kind === "null" ? null : v.kind === "binary" ? `0x${v.hex ?? ""}` : v.text)))}`
    );
    return req.mode === "copy" ? [`CLEAR ${table}`, ...rows] : rows;
  });
}

/**
 * 让建表、删表、清空、建库的绑定像驱动一样给出语句:内容是请求本身,一眼能与任何真实方言区分,
 * 测试据此断言前端发出的意图与执行的语句。
 */
export function mockSchemaStatements(): void {
  vi.mocked(GetCreateTableStatements).mockImplementation(async (_assetId, database, req) => [
    `CREATE TABLE ${database}.${req.name} ${JSON.stringify(req.columns)}`,
  ]);
  vi.mocked(GetDropTableStatements).mockImplementation(async (_assetId, database, table) => [
    `DROP ${database}.${table}`,
  ]);
  vi.mocked(GetTruncateTableStatements).mockImplementation(async (_assetId, database, table) => [
    `TRUNCATE ${database}.${table}`,
  ]);
  vi.mocked(GetCreateDatabaseStatements).mockImplementation(async (_assetId, _database, req) => [
    `CREATE DATABASE ${req.name} ${JSON.stringify(req.options)}`,
  ]);
}

/** 让 Query.ListDatabaseDrivers 返回给定的描述列表,并丢弃进程内缓存。 */
export function provideDrivers(drivers: dbdriver.Descriptor[]): void {
  refreshDatabaseDrivers();
  vi.mocked(ListDatabaseDrivers).mockResolvedValue(drivers);
}
