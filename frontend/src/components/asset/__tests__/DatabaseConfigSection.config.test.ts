import { describe, it, expect } from "vitest";
import {
  buildDatabaseConfig,
  parseDatabaseConfig,
  applyDriverChange,
  driverIcon,
  DATABASE_DEFAULTS,
  type DatabaseFormState,
} from "@/components/asset/DatabaseConfigSection.config";

const FULL_MYSQL: DatabaseFormState = {
  driver: "mysql",
  host: "db.example.com",
  port: 3306,
  username: "root",
  database: "mydb",
  sslMode: "disable",
  tls: true,
  readOnly: true,
  params: "charset=utf8mb4",
  path: "",
  sshTunnelId: 5,
};

const FULL_PG: DatabaseFormState = {
  driver: "postgresql",
  host: "pg.example.com",
  port: 5432,
  username: "postgres",
  database: "mydb",
  sslMode: "require",
  tls: false,
  readOnly: false,
  params: "",
  path: "",
  sshTunnelId: 0,
};

const FULL_MSSQL: DatabaseFormState = {
  driver: "mssql",
  host: "mssql.example.com",
  port: 1433,
  username: "sa",
  database: "mydb",
  sslMode: "disable",
  tls: true,
  readOnly: false,
  params: "",
  path: "",
  sshTunnelId: 0,
};

const FULL_SQLITE: DatabaseFormState = {
  driver: "sqlite",
  host: "ignored.example.com",
  port: 1234,
  username: "ignored",
  database: "main",
  sslMode: "verify-full",
  tls: true,
  readOnly: true,
  params: "mode=ro",
  path: "/tmp/data.db",
  sshTunnelId: 9,
};

describe("buildDatabaseConfig (键序锁旧 save: driver→[sqlite:path | host/port/username/credential/ssh/ssl_mode/tls]→database/read_only/params)", () => {
  it("mysql 全字段 + inline password + tls", () => {
    expect(buildDatabaseConfig(FULL_MYSQL, { password: "ENC" })).toBe(
      '{"driver":"mysql","host":"db.example.com","port":3306,"username":"root","password":"ENC",' +
        '"ssh_asset_id":5,"tls":true,"database":"mydb","read_only":true,"params":"charset=utf8mb4"}'
    );
  });

  it("mysql + managed 凭据 → credential_id 紧跟 username,在 ssh_asset_id 前", () => {
    expect(buildDatabaseConfig(FULL_MYSQL, { credential_id: 7 })).toContain(
      '"username":"root","credential_id":7,"ssh_asset_id":5'
    );
  });

  it("postgresql 带 ssl_mode(require)", () => {
    expect(buildDatabaseConfig(FULL_PG, { password: "ENC" })).toBe(
      '{"driver":"postgresql","host":"pg.example.com","port":5432,"username":"postgres","password":"ENC",' +
        '"ssl_mode":"require","database":"mydb"}'
    );
  });

  it("postgresql sslMode=disable 时省略 ssl_mode", () => {
    const json = buildDatabaseConfig({ ...FULL_PG, sslMode: "disable" }, {});
    expect(json).not.toContain("ssl_mode");
  });

  it("postgresql 不写 tls(即便 tls=true)", () => {
    const json = buildDatabaseConfig({ ...FULL_PG, tls: true }, {});
    expect(json).not.toContain('"tls"');
  });

  it("mssql 带 tls", () => {
    expect(buildDatabaseConfig(FULL_MSSQL, { password: "ENC" })).toBe(
      '{"driver":"mssql","host":"mssql.example.com","port":1433,"username":"sa","password":"ENC",' +
        '"tls":true,"database":"mydb"}'
    );
  });

  it("mssql 不写 ssl_mode(即便 sslMode 非 disable)", () => {
    const json = buildDatabaseConfig({ ...FULL_MSSQL, sslMode: "require" }, {});
    expect(json).not.toContain("ssl_mode");
  });

  it("mysql tls=false 时省略 tls", () => {
    const json = buildDatabaseConfig({ ...FULL_MYSQL, tls: false }, {});
    expect(json).not.toContain('"tls"');
  });

  it("sqlite 仅 path,忽略凭据/host/port/ssh/ssl/tls", () => {
    expect(buildDatabaseConfig(FULL_SQLITE, { credential_id: 7, password: "ENC" })).toBe(
      '{"driver":"sqlite","path":"/tmp/data.db","database":"main","read_only":true,"params":"mode=ro"}'
    );
  });

  it("sqlite 分支不含 host/port/username/credential/ssh/ssl_mode/tls 键", () => {
    const json = buildDatabaseConfig(FULL_SQLITE, { credential_id: 7 });
    for (const key of ["host", "port", "username", "credential_id", "password", "ssh_asset_id", "ssl_mode", "tls"]) {
      expect(json).not.toContain(`"${key}"`);
    }
  });

  it("最小 mysql 态(host+port+username,空凭据)", () => {
    expect(buildDatabaseConfig({ ...DATABASE_DEFAULTS, host: "127.0.0.1", username: "u" }, {})).toBe(
      '{"driver":"mysql","host":"127.0.0.1","port":3306,"username":"u"}'
    );
  });

  it("空凭据片段不写 password / credential_id 键", () => {
    const json = buildDatabaseConfig({ ...DATABASE_DEFAULTS, host: "127.0.0.1" }, {});
    expect(json).not.toContain("password");
    expect(json).not.toContain("credential_id");
  });

  it("sshTunnelId=0 时省略 ssh_asset_id", () => {
    const json = buildDatabaseConfig({ ...FULL_MYSQL, sshTunnelId: 0 }, {});
    expect(json).not.toContain("ssh_asset_id");
  });

  it("database 为空时省略", () => {
    const json = buildDatabaseConfig({ ...FULL_MYSQL, database: "" }, {});
    expect(json).not.toContain('"database"');
  });

  it("readOnly=false 时省略 read_only", () => {
    const json = buildDatabaseConfig({ ...FULL_MYSQL, readOnly: false }, {});
    expect(json).not.toContain("read_only");
  });

  it("params 为空时省略", () => {
    const json = buildDatabaseConfig({ ...FULL_MYSQL, params: "" }, {});
    expect(json).not.toContain("params");
  });

  it("managed 凭据优先于 password(credential_id 存在则不写 password)", () => {
    const json = buildDatabaseConfig(FULL_MYSQL, { credential_id: 7, password: "ENC" });
    expect(json).toContain('"credential_id":7');
    expect(json).not.toContain('"password"');
  });
});

describe("parseDatabaseConfig (镜像旧 loadDatabaseConfig 非凭据字段)", () => {
  it("mysql 全字段回填(无凭据)", () => {
    expect(
      parseDatabaseConfig(
        '{"driver":"mysql","host":"db.example.com","port":3306,"username":"root",' +
          '"ssh_asset_id":5,"tls":true,"database":"mydb","read_only":true,"params":"charset=utf8mb4"}'
      )
    ).toEqual({
      driver: "mysql",
      host: "db.example.com",
      port: 3306,
      username: "root",
      database: "mydb",
      sslMode: "disable",
      tls: true,
      readOnly: true,
      params: "charset=utf8mb4",
      path: "",
      sshTunnelId: 5,
    });
  });

  it("postgresql 带 ssl_mode 回填", () => {
    const s = parseDatabaseConfig('{"driver":"postgresql","host":"h","port":5432,"ssl_mode":"require"}');
    expect(s.driver).toBe("postgresql");
    expect(s.sslMode).toBe("require");
  });

  it("sqlite path 回填,port 默认 3306", () => {
    const s = parseDatabaseConfig('{"driver":"sqlite","path":"/tmp/x.db"}');
    expect(s.driver).toBe("sqlite");
    expect(s.path).toBe("/tmp/x.db");
    expect(s.port).toBe(3306);
    expect(s.host).toBe("");
  });

  it("缺字段用默认", () => {
    expect(parseDatabaseConfig("{}")).toEqual(DATABASE_DEFAULTS);
  });

  it("非法 JSON 回退默认", () => {
    expect(parseDatabaseConfig("nope")).toEqual(DATABASE_DEFAULTS);
  });

  it("parse→build 往返(mysql 全字段,inline 密文沿用)", () => {
    const original =
      '{"driver":"mysql","host":"db.example.com","port":3306,"username":"root","password":"OLD",' +
      '"ssh_asset_id":5,"tls":true,"database":"mydb","read_only":true,"params":"charset=utf8mb4"}';
    const state = parseDatabaseConfig(original);
    expect(buildDatabaseConfig(state, { password: "OLD" })).toBe(original);
  });

  it("parse→build 往返(postgresql ssl_mode)", () => {
    const original =
      '{"driver":"postgresql","host":"pg.example.com","port":5432,"username":"postgres","password":"OLD",' +
      '"ssl_mode":"require","database":"mydb"}';
    const state = parseDatabaseConfig(original);
    expect(buildDatabaseConfig(state, { password: "OLD" })).toBe(original);
  });

  it("parse→build 往返(sqlite path)", () => {
    const original = '{"driver":"sqlite","path":"/tmp/x.db","database":"main","read_only":true}';
    const state = parseDatabaseConfig(original);
    expect(buildDatabaseConfig(state, {})).toBe(original);
  });
});

describe("applyDriverChange (镜像旧 handleDriverChange section 自有字段)", () => {
  it("mysql→sqlite 清 host/username/ssh,port=0", () => {
    const next = applyDriverChange(FULL_MYSQL, "sqlite");
    expect(next.driver).toBe("sqlite");
    expect(next.host).toBe("");
    expect(next.port).toBe(0);
    expect(next.username).toBe("");
    expect(next.sshTunnelId).toBe(0);
  });

  it("sqlite→postgresql 设 port=5432,清 path,sslMode 保留", () => {
    const sqliteState = applyDriverChange(FULL_MYSQL, "sqlite");
    const next = applyDriverChange({ ...sqliteState, path: "/tmp/x.db", sslMode: "require" }, "postgresql");
    expect(next.driver).toBe("postgresql");
    expect(next.port).toBe(5432);
    expect(next.path).toBe("");
    expect(next.sslMode).toBe("require");
  });

  it("mysql→mssql 设 port=1433,sslMode 复位 disable(非 postgresql)", () => {
    const next = applyDriverChange({ ...FULL_MYSQL, sslMode: "require" }, "mssql");
    expect(next.port).toBe(1433);
    expect(next.sslMode).toBe("disable");
  });

  it("postgresql→mysql 设 port=3306,sslMode 复位 disable", () => {
    const next = applyDriverChange({ ...FULL_PG, sslMode: "verify-full" }, "mysql");
    expect(next.port).toBe(3306);
    expect(next.sslMode).toBe("disable");
  });

  it("→postgresql 保留 sslMode(不复位)", () => {
    const next = applyDriverChange({ ...FULL_MYSQL, sslMode: "verify-ca" }, "postgresql");
    expect(next.sslMode).toBe("verify-ca");
  });
});

describe("driverIcon (镜像旧 DEFAULT_ICONS)", () => {
  it("各 driver 映射", () => {
    expect(driverIcon("mysql")).toBe("mysql");
    expect(driverIcon("postgresql")).toBe("postgresql");
    expect(driverIcon("mssql")).toBe("database");
    expect(driverIcon("sqlite")).toBe("sqlite");
  });
});
