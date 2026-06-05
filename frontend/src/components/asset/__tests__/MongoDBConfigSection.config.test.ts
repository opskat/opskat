import { describe, it, expect } from "vitest";
import {
  buildMongoDBConfig,
  parseMongoDBConfig,
  MONGODB_DEFAULTS,
  type MongoDBFormState,
} from "@/components/asset/MongoDBConfigSection.config";

const FULL_MANUAL: MongoDBFormState = {
  connectionMode: "manual",
  connectionURI: "",
  host: "mongo.example.com",
  port: 27017,
  username: "admin",
  replicaSet: "rs0",
  authSource: "admin",
  database: "mydb",
  tls: true,
  sshTunnelId: 5,
};

const FULL_URI: MongoDBFormState = {
  connectionMode: "uri",
  connectionURI: "mongodb://user:pass@host:27017/db",
  host: "ignored.example.com",
  port: 27017,
  username: "admin",
  replicaSet: "rs0",
  authSource: "admin",
  database: "mydb",
  tls: true,
  sshTunnelId: 5,
};

describe("buildMongoDBConfig (键序锁旧 save;save 省略 ssh_asset_id 走 asset 顶层列,test 传 includeSshAssetId 才写)", () => {
  it("manual 全字段 + inline password(save:无 ssh_asset_id)", () => {
    expect(buildMongoDBConfig(FULL_MANUAL, { password: "ENC" })).toBe(
      '{"host":"mongo.example.com","port":27017,"username":"admin","password":"ENC",' +
        '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true}'
    );
  });
  it("save 路径(默认)省略 ssh_asset_id —— 隧道走 asset 顶层列(锁旧 save)", () => {
    expect(buildMongoDBConfig(FULL_MANUAL, {})).not.toContain("ssh_asset_id");
  });
  it("test 路径(includeSshAssetId=true)在末尾写 ssh_asset_id(锁旧 handleTestMongoDBConnection)", () => {
    expect(buildMongoDBConfig(FULL_MANUAL, { password: "ENC" }, true)).toContain('"tls":true,"ssh_asset_id":5}');
  });

  it("manual + managed 凭据 → credential_id 紧跟 username", () => {
    expect(buildMongoDBConfig(FULL_MANUAL, { credential_id: 7 })).toContain(
      '"username":"admin","credential_id":7,"replica_set":"rs0"'
    );
  });

  it("uri 模式 → connection_uri 为首键,host/port 省略", () => {
    const json = buildMongoDBConfig(FULL_URI, { password: "ENC" });
    expect(json).toContain('"connection_uri":"mongodb://user:pass@host:27017/db"');
    expect(json).not.toContain('"host":');
    expect(json).not.toContain('"port":');
  });

  it("uri 模式全字段键序(save:无 ssh_asset_id)", () => {
    expect(buildMongoDBConfig(FULL_URI, { password: "ENC" })).toBe(
      '{"connection_uri":"mongodb://user:pass@host:27017/db","username":"admin","password":"ENC",' +
        '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true}'
    );
  });

  it("uri 空时降级为 manual(connectionMode=uri 但 connectionURI 为空)", () => {
    const s: MongoDBFormState = { ...FULL_URI, connectionURI: "" };
    const json = buildMongoDBConfig(s, {});
    // uri 为空 → 走 else 分支 → 写 host/port
    expect(json).toContain('"host":"ignored.example.com"');
    expect(json).not.toContain("connection_uri");
  });

  it("最小 manual 态(仅 host+port)", () => {
    expect(buildMongoDBConfig({ ...MONGODB_DEFAULTS, host: "127.0.0.1" }, {})).toBe(
      '{"host":"127.0.0.1","port":27017}'
    );
  });

  it("空凭据片段不写 password / credential_id 键", () => {
    const json = buildMongoDBConfig({ ...MONGODB_DEFAULTS, host: "127.0.0.1" }, {});
    expect(json).not.toContain("password");
    expect(json).not.toContain("credential_id");
  });

  it("tls=false 时省略 tls 键", () => {
    const json = buildMongoDBConfig({ ...FULL_MANUAL, tls: false }, {});
    expect(json).not.toContain('"tls"');
  });

  it("replica_set 为空时省略", () => {
    const json = buildMongoDBConfig({ ...FULL_MANUAL, replicaSet: "" }, {});
    expect(json).not.toContain("replica_set");
  });

  it("auth_source 为空时省略", () => {
    const json = buildMongoDBConfig({ ...FULL_MANUAL, authSource: "" }, {});
    expect(json).not.toContain("auth_source");
  });

  it("database 为空时省略", () => {
    const json = buildMongoDBConfig({ ...FULL_MANUAL, database: "" }, {});
    expect(json).not.toContain('"database"');
  });
});

describe("parseMongoDBConfig (镜像旧 loadMongoDBConfig 非凭据字段)", () => {
  it("manual 全字段回填", () => {
    expect(
      parseMongoDBConfig(
        '{"host":"mongo.example.com","port":27017,"username":"admin",' +
          '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true,"ssh_asset_id":5}'
      )
    ).toEqual({
      connectionMode: "manual",
      connectionURI: "",
      host: "mongo.example.com",
      port: 27017,
      username: "admin",
      replicaSet: "rs0",
      authSource: "admin",
      database: "mydb",
      tls: true,
      sshTunnelId: 5,
    });
  });

  it("connection_uri 存在时 → connectionMode=uri", () => {
    const s = parseMongoDBConfig('{"connection_uri":"mongodb://host:27017/db"}');
    expect(s.connectionMode).toBe("uri");
    expect(s.connectionURI).toBe("mongodb://host:27017/db");
  });

  it("无 connection_uri 时 → connectionMode=manual", () => {
    const s = parseMongoDBConfig('{"host":"h","port":27017}');
    expect(s.connectionMode).toBe("manual");
    expect(s.connectionURI).toBe("");
  });

  it("缺字段用默认", () => {
    expect(parseMongoDBConfig("{}")).toEqual(MONGODB_DEFAULTS);
  });

  it("非法 JSON 回退默认", () => {
    expect(parseMongoDBConfig("nope")).toEqual(MONGODB_DEFAULTS);
  });

  it("parse→build 往返(manual 全字段;saved config 无 ssh_asset_id,隧道走 asset 顶层列)", () => {
    const original =
      '{"host":"mongo.example.com","port":27017,"username":"admin",' +
      '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true}';
    const state = parseMongoDBConfig(original);
    expect(buildMongoDBConfig(state, {})).toBe(original);
  });

  it("parse→build 往返(uri 模式)", () => {
    const original = '{"connection_uri":"mongodb://user:pass@host:27017/db","username":"admin"}';
    const state = parseMongoDBConfig(original);
    expect(buildMongoDBConfig(state, {})).toBe(original);
  });
});
