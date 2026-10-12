import { dbdriver } from "../../wailsjs/go/models";

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

const NETWORK_CONNECTION = ["host", "port", "username", "password", "database"];
const ADVANCED = ["params", "read_only", "query_timeout_seconds"];

/** 后端内置四个驱动描述的镜像(internal/dbdriver/{mysql,postgresql,mssql,sqlite}),供前端测试当作后端返回值。 */
export const BUILTIN_DRIVERS: dbdriver.Descriptor[] = [
  driver({
    id: "mysql",
    name: "MySQL",
    icon: "mysql",
    order: 10,
    defaultPort: 3306,
    form: { connection: NETWORK_CONNECTION, tunnel: ["tunnel"], tls: ["tls"], advanced: ADVANCED },
  }),
  driver({
    id: "postgresql",
    name: "PostgreSQL",
    icon: "postgresql",
    order: 20,
    defaultPort: 5432,
    form: { connection: NETWORK_CONNECTION, tunnel: ["tunnel"], tls: ["ssl_mode"], advanced: ADVANCED },
  }),
  driver({
    id: "mssql",
    name: "SQL Server",
    icon: "sqlserver",
    order: 30,
    defaultPort: 1433,
    form: { connection: NETWORK_CONNECTION, tunnel: ["tunnel"], tls: ["tls"], advanced: ADVANCED },
  }),
  driver({
    id: "sqlite",
    name: "SQLite",
    icon: "sqlite",
    order: 40,
    defaultPort: 0,
    form: { connection: ["sqlite_source", "path", "ssh_asset_id"], tunnel: ["tunnel"], advanced: ADVANCED },
  }),
];

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
