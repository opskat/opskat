import { describe, it, expect } from "vitest";
import {
  buildRedisConfig,
  parseRedisConfig,
  REDIS_DEFAULTS,
  type RedisFormState,
} from "@/components/asset/RedisConfigSection.config";

const FULL: RedisFormState = {
  host: "redis.example.com",
  port: 6379,
  username: "admin",
  database: 2,
  commandTimeoutSeconds: 30,
  scanPageSize: 200,
  keySeparator: ":",
  tls: true,
  tlsInsecure: true,
  tlsServerName: "redis.x",
  tlsCAFile: "/ca.pem",
  tlsCertFile: "/c.crt",
  tlsKeyFile: "/c.key",
  sshTunnelId: 3,
};

describe("buildRedisConfig (锁旧 save 序;save 省略 ssh_asset_id 走 asset 顶层列,test 传 includeSshAssetId 才写)", () => {
  it("全字段 + 既加密 password(save:无 ssh_asset_id)", () => {
    expect(buildRedisConfig(FULL, { password: "ENC" })).toBe(
      '{"host":"redis.example.com","port":6379,"username":"admin","password":"ENC",' +
        '"database":2,"tls":true,"tls_insecure":true,"tls_server_name":"redis.x","tls_ca_file":"/ca.pem",' +
        '"tls_cert_file":"/c.crt","tls_key_file":"/c.key","command_timeout_seconds":30,' +
        '"scan_page_size":200}'
    );
  });
  it("save 路径(默认)省略 ssh_asset_id —— 隧道走 asset 顶层列(锁旧 save)", () => {
    expect(buildRedisConfig(FULL, {})).not.toContain("ssh_asset_id");
  });
  it("test 路径(includeSshAssetId=true)在末尾写 ssh_asset_id(锁旧 handleTestRedisConnection)", () => {
    expect(buildRedisConfig(FULL, { password: "ENC" }, true)).toContain('"scan_page_size":200,"ssh_asset_id":3}');
  });
  it("managed 凭据 → credential_id 紧跟 username", () => {
    expect(buildRedisConfig(FULL, { credential_id: 7 })).toContain('"username":"admin","credential_id":7,"database":2');
  });
  it("最小态(仅 host+port,默认超时/scanPageSize 仍写)", () => {
    expect(buildRedisConfig({ ...REDIS_DEFAULTS, host: "127.0.0.1" }, {})).toBe(
      '{"host":"127.0.0.1","port":6379,"command_timeout_seconds":30,"scan_page_size":200}'
    );
  });
  it("tls=false 时省略全部 tls_* 子键", () => {
    const s = { ...FULL, tls: false };
    const json = buildRedisConfig(s, {});
    expect(json).not.toContain("tls_insecure");
    expect(json).not.toContain("tls_server_name");
    expect(json).not.toContain('"tls":');
  });
  it("空凭据片段不写 password / credential_id 键", () => {
    const json = buildRedisConfig({ ...REDIS_DEFAULTS, host: "127.0.0.1" }, {});
    expect(json).not.toContain("password");
    expect(json).not.toContain("credential_id");
  });
  it("key_separator 为默认 ':' 时省略该键", () => {
    const json = buildRedisConfig({ ...REDIS_DEFAULTS, host: "h", keySeparator: ":" }, {});
    expect(json).not.toContain("key_separator");
  });
  it("key_separator 非默认时写入", () => {
    const json = buildRedisConfig({ ...REDIS_DEFAULTS, host: "h", keySeparator: "/" }, {});
    expect(json).toContain('"key_separator":"/"');
  });
  it("database=0 时省略该键", () => {
    const json = buildRedisConfig({ ...REDIS_DEFAULTS, host: "h", database: 0 }, {});
    expect(json).not.toContain("database");
  });
  it("commandTimeoutSeconds=0 scanPageSize=0 时省略对应键", () => {
    const json = buildRedisConfig({ ...REDIS_DEFAULTS, host: "h", commandTimeoutSeconds: 0, scanPageSize: 0 }, {});
    expect(json).toBe('{"host":"h","port":6379}');
  });
});

describe("parseRedisConfig (锁旧 loadRedisConfig 非凭据字段)", () => {
  it("全字段回填(ssh_asset_id 仅来自 config)", () => {
    expect(
      parseRedisConfig(
        '{"host":"redis.example.com","port":6380,"username":"u","tls":true,"tls_insecure":true,' +
          '"tls_server_name":"sn","tls_ca_file":"/ca","tls_cert_file":"/cc","tls_key_file":"/ck",' +
          '"database":3,"command_timeout_seconds":60,"scan_page_size":100,"key_separator":"/","ssh_asset_id":5}'
      )
    ).toEqual({
      host: "redis.example.com",
      port: 6380,
      username: "u",
      tls: true,
      tlsInsecure: true,
      tlsServerName: "sn",
      tlsCAFile: "/ca",
      tlsCertFile: "/cc",
      tlsKeyFile: "/ck",
      database: 3,
      commandTimeoutSeconds: 60,
      scanPageSize: 100,
      keySeparator: "/",
      sshTunnelId: 5,
    });
  });
  it("缺字段用默认", () => {
    expect(parseRedisConfig("{}")).toEqual(REDIS_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseRedisConfig("nope")).toEqual(REDIS_DEFAULTS);
  });
  it("key_separator 缺省回填 ':'", () => {
    expect(parseRedisConfig('{"host":"h","port":6379}').keySeparator).toBe(":");
  });
});
