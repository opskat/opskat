import { describe, it, expect } from "vitest";
import {
  buildRedisConfig,
  listedMappingAnnouncedAddresses,
  nodeAddressMapValidationKey,
  parseNodeAddressMap,
  parseRedisConfig,
  parseRedisNodes,
  redisModeRequiredKey,
  resolveSaveSentinelPassword,
  resolveTestSentinelPassword,
  REDIS_DEFAULTS,
  type RedisFormState,
} from "@/components/asset/RedisConfigSection.config";
import { CONNECTION_DEFAULTS, socks5ProxyLayer, sshProxyLayer } from "@/components/asset/proxyConfig";

const MODE_DEFAULTS = {
  mode: "standalone" as const,
  nodes: "",
  masterName: "",
  sentinelUsername: "",
  sentinelPassword: "",
  encryptedSentinelPassword: "",
  nodeAddressMap: "",
};

const FULL: RedisFormState = {
  ...CONNECTION_DEFAULTS,
  ...MODE_DEFAULTS,
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
  connectionType: "jumphost",
  sshTunnelId: 3,
};

const PROXY: RedisFormState = {
  ...FULL,
  connectionType: "proxy",
  sshTunnelId: 0,
  proxyHost: "p.example.com",
  proxyPort: 1081,
  proxyUsername: "pu",
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

  it("proxy 模式写 proxy 不写 ssh_asset_id(键序: tls_* 后、尾部公共键前)", () => {
    expect(buildRedisConfig(PROXY, { password: "ENC" }, true, "PROXYENC")).toBe(
      '{"host":"redis.example.com","port":6379,"username":"admin","password":"ENC",' +
        '"database":2,"tls":true,"tls_insecure":true,"tls_server_name":"redis.x","tls_ca_file":"/ca.pem",' +
        '"tls_cert_file":"/c.crt","tls_key_file":"/c.key",' +
        '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"},' +
        '"command_timeout_seconds":30,"scan_page_size":200}'
    );
  });

  it("jumphost 模式不写 proxy(互斥,即便 proxy 字段有值)", () => {
    const json = buildRedisConfig({ ...PROXY, connectionType: "jumphost", sshTunnelId: 3 }, {}, true, "PROXYENC");
    expect(json).toContain('"ssh_asset_id":3');
    expect(json).not.toContain('"proxy"');
  });

  it("direct 模式不写 proxy 也不写 ssh_asset_id(即便 includeSshAssetId=true)", () => {
    const json = buildRedisConfig({ ...PROXY, connectionType: "direct" }, {}, true, "PROXYENC");
    expect(json).not.toContain('"proxy"');
    expect(json).not.toContain("ssh_asset_id");
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
      ...CONNECTION_DEFAULTS,
      ...MODE_DEFAULTS,
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
      connectionType: "jumphost",
      sshTunnelId: 5,
      proxyChainLayers: [sshProxyLayer(5, "SSH Tunnel", "legacy-ssh-5")],
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
  it("带 proxy 回填并派生 connectionType=proxy(密码入 encrypted)", () => {
    const s = parseRedisConfig(
      '{"host":"h","port":6379,' +
        '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"}}'
    );
    expect(s.connectionType).toBe("proxy");
    expect(s.proxyHost).toBe("p.example.com");
    expect(s.proxyPort).toBe(1081);
    expect(s.proxyUsername).toBe("pu");
    expect(s.proxyPassword).toBe("");
    expect(s.encryptedProxyPassword).toBe("PROXYENC");
    expect(s.proxyChainLayers).toEqual([
      socks5ProxyLayer({
        type: "socks5",
        host: "p.example.com",
        port: 1081,
        username: "pu",
        password: "PROXYENC",
      }),
    ]);
  });
  it("assetTunnelId 入参优先派生 jumphost(镜像 asset.sshTunnelId 优先)", () => {
    const s = parseRedisConfig('{"host":"h","proxy":{"type":"socks5","host":"p","port":1080}}', 6);
    expect(s.connectionType).toBe("jumphost");
    expect(s.sshTunnelId).toBe(6);
  });
  it("parse→build 往返(proxy,密文沿用)", () => {
    const original =
      '{"host":"redis.example.com","port":6379,"username":"u","password":"OLD",' +
      '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"},' +
      '"command_timeout_seconds":30,"scan_page_size":200}';
    const expected =
      '{"host":"redis.example.com","port":6379,"username":"u","password":"OLD",' +
      '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"},' +
      '"proxy_chain":{"layers":[{"id":"legacy-socks5-proxy","name":"SOCKS5 Proxy","enabled":true,"type":"socks5","order":1,"host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"}]},' +
      '"command_timeout_seconds":30,"scan_page_size":200}';
    const state = parseRedisConfig(original);
    expect(buildRedisConfig(state, { password: "OLD" }, false, state.encryptedProxyPassword)).toBe(expected);
  });
});

describe("buildRedisConfig (集群 / 哨兵模式:只序列化当前模式字段)", () => {
  it("集群模式写 mode/nodes,不写 host/port/database(即便 state 仍留有旧值)", () => {
    const s: RedisFormState = {
      ...REDIS_DEFAULTS,
      mode: "cluster",
      host: "leftover.example.com",
      port: 6379,
      database: 2,
      nodes: "10.0.0.1:7001\n10.0.0.2:7001",
    };
    expect(buildRedisConfig(s, {})).toBe(
      '{"command_timeout_seconds":30,"scan_page_size":200,"mode":"cluster","nodes":["10.0.0.1:7001","10.0.0.2:7001"]}'
    );
  });

  it("哨兵模式写 mode/nodes/master_name,数据库保留(非集群)", () => {
    const s: RedisFormState = {
      ...REDIS_DEFAULTS,
      mode: "sentinel",
      nodes: "10.0.0.31:26379",
      masterName: "mymaster",
      database: 1,
    };
    expect(buildRedisConfig(s, {})).toBe(
      '{"database":1,"command_timeout_seconds":30,"scan_page_size":200,' +
        '"mode":"sentinel","nodes":["10.0.0.31:26379"],"master_name":"mymaster"}'
    );
  });

  it("哨兵认证字段(用户名/密码片段)只在哨兵模式写", () => {
    const s: RedisFormState = {
      ...REDIS_DEFAULTS,
      mode: "sentinel",
      nodes: "10.0.0.31:26379",
      masterName: "mymaster",
      sentinelUsername: "sentinel-admin",
    };
    const json = buildRedisConfig(s, {}, false, "", undefined, { sentinel_password: "SENTINEL_ENC" });
    expect(json).toContain('"sentinel_username":"sentinel-admin"');
    expect(json).toContain('"sentinel_password":"SENTINEL_ENC"');
  });

  it("集群/哨兵模式写 node_address_map(留空右侧的行不写入)", () => {
    const s: RedisFormState = {
      ...REDIS_DEFAULTS,
      mode: "cluster",
      nodes: "172.18.0.11:6379",
      nodeAddressMap: "172.18.0.11:6379 = 10.20.0.5:7001\n172.18.0.12:6379 = ",
    };
    const json = buildRedisConfig(s, {});
    expect(json).toContain('"node_address_map":{"172.18.0.11:6379":"10.20.0.5:7001"}');
    expect(json).not.toContain("172.18.0.12");
  });

  it("standalone 模式不受新字段影响(与旧版逐字节相同)", () => {
    const s: RedisFormState = { ...REDIS_DEFAULTS, host: "127.0.0.1", mode: "standalone", nodes: "leftover:1" };
    expect(buildRedisConfig(s, {})).toBe(
      '{"host":"127.0.0.1","port":6379,"command_timeout_seconds":30,"scan_page_size":200}'
    );
  });
});

describe("parseRedisConfig (集群 / 哨兵模式回填)", () => {
  it("集群配置回填 mode/nodes", () => {
    const s = parseRedisConfig('{"mode":"cluster","nodes":["10.0.0.1:7001","10.0.0.2:7001"]}');
    expect(s.mode).toBe("cluster");
    expect(s.nodes).toBe("10.0.0.1:7001\n10.0.0.2:7001");
  });

  it("哨兵配置回填 master_name/sentinel_username/既有哨兵密文(不回填明文)", () => {
    const s = parseRedisConfig(
      '{"mode":"sentinel","nodes":["10.0.0.31:26379"],"master_name":"mymaster",' +
        '"sentinel_username":"su","sentinel_password":"SENTINEL_ENC"}'
    );
    expect(s.masterName).toBe("mymaster");
    expect(s.sentinelUsername).toBe("su");
    expect(s.sentinelPassword).toBe("");
    expect(s.encryptedSentinelPassword).toBe("SENTINEL_ENC");
  });

  it("node_address_map 回填为「宣告 = 实际」多行文本", () => {
    const s = parseRedisConfig('{"mode":"cluster","node_address_map":{"172.18.0.11:6379":"10.20.0.5:7001"}}');
    expect(s.nodeAddressMap).toBe("172.18.0.11:6379 = 10.20.0.5:7001");
  });

  it("未知/缺省 mode 按 standalone 处理", () => {
    expect(parseRedisConfig("{}").mode).toBe("standalone");
    expect(parseRedisConfig('{"mode":"bogus"}').mode).toBe("standalone");
  });
});

describe("parseRedisNodes (每行一个 host:port,逗号/分号亦可分隔)", () => {
  it("换行/逗号/分号分隔并去空白", () => {
    expect(parseRedisNodes("10.0.0.1:7001\n10.0.0.2:7001, 10.0.0.3:7001;10.0.0.4:7001\n\n")).toEqual([
      "10.0.0.1:7001",
      "10.0.0.2:7001",
      "10.0.0.3:7001",
      "10.0.0.4:7001",
    ]);
  });
  it("空文本返回空数组", () => {
    expect(parseRedisNodes("")).toEqual([]);
  });
});

describe("parseNodeAddressMap / nodeAddressMapValidationKey (禁止保存 + 指出哪一行)", () => {
  it("正常行解析为 map,空白行跳过", () => {
    const r = parseNodeAddressMap("172.18.0.11:6379 = 10.20.0.5:7001\n\n172.18.0.12:6379 = 10.20.0.5:7002");
    expect(r.map).toEqual({ "172.18.0.11:6379": "10.20.0.5:7001", "172.18.0.12:6379": "10.20.0.5:7002" });
    expect(r.error).toBeUndefined();
  });
  it("右侧留空视为未完成,不算错误也不写入 map", () => {
    const r = parseNodeAddressMap("172.18.0.11:6379 = ");
    expect(r.map).toEqual({});
    expect(r.error).toBeUndefined();
  });
  it("缺少 `=` 视为格式错误,指出行号(1 基)", () => {
    const r = parseNodeAddressMap("172.18.0.11:6379 = 10.20.0.5:7001\nnot-a-valid-line");
    expect(r.error).toEqual({ line: 2, key: "asset.redisMappingLineInvalid" });
  });
  it("同一宣告地址出现两次视为重复,指出第二次出现的行号", () => {
    const r = parseNodeAddressMap("172.18.0.11:6379 = a:1\n172.18.0.11:6379 = b:2");
    expect(r.error).toEqual({ line: 2, key: "asset.redisMappingDuplicate" });
  });
  it("nodeAddressMapValidationKey 转扁平 key,无误时为空串", () => {
    expect(nodeAddressMapValidationKey("bad-line")).toBe("asset.redisMappingLineInvalid");
    expect(nodeAddressMapValidationKey("a:1 = b:2")).toBe("");
  });
});

describe("listedMappingAnnouncedAddresses (生成映射去重用)", () => {
  it("含已填与未填(占位)行的宣告地址都计入", () => {
    const set = listedMappingAnnouncedAddresses("172.18.0.11:6379 = 10.20.0.5:7001\n172.18.0.12:6379 = ");
    expect(set).toEqual(new Set(["172.18.0.11:6379", "172.18.0.12:6379"]));
  });
});

describe("redisModeRequiredKey (模式必填,镜像后端 validateMode)", () => {
  it("standalone 缺 host", () => {
    expect(redisModeRequiredKey({ ...REDIS_DEFAULTS, host: "" })).toBe("asset.formMissingHost");
  });
  it("cluster 缺节点", () => {
    expect(redisModeRequiredKey({ ...REDIS_DEFAULTS, mode: "cluster", nodes: "" })).toBe("asset.redisNodesRequired");
  });
  it("sentinel 缺节点", () => {
    expect(redisModeRequiredKey({ ...REDIS_DEFAULTS, mode: "sentinel", nodes: "", masterName: "m" })).toBe(
      "asset.redisNodesRequired"
    );
  });
  it("sentinel 有节点但缺 master_name", () => {
    expect(
      redisModeRequiredKey({ ...REDIS_DEFAULTS, mode: "sentinel", nodes: "10.0.0.31:26379", masterName: "" })
    ).toBe("asset.redisMasterNameRequired");
  });
  it("字段齐全时为空串", () => {
    expect(
      redisModeRequiredKey({ ...REDIS_DEFAULTS, mode: "sentinel", nodes: "10.0.0.31:26379", masterName: "m" })
    ).toBe("");
    expect(redisModeRequiredKey({ ...REDIS_DEFAULTS, mode: "cluster", nodes: "10.0.0.1:7001" })).toBe("");
  });
});

describe("resolveTestSentinelPassword / resolveSaveSentinelPassword", () => {
  it("测试:无新明文但有既有密文时沿用既有密文", () => {
    expect(resolveTestSentinelPassword({ ...REDIS_DEFAULTS, encryptedSentinelPassword: "ENC" })).toEqual({
      sentinel_password: "ENC",
    });
  });
  it("测试:有新明文时不写(走 plainSentinelPassword 单独传)", () => {
    expect(
      resolveTestSentinelPassword({ ...REDIS_DEFAULTS, sentinelPassword: "new", encryptedSentinelPassword: "ENC" })
    ).toEqual({});
  });
  it("测试:都无时不写", () => {
    expect(resolveTestSentinelPassword(REDIS_DEFAULTS)).toEqual({});
  });
  it("保存:新明文时加密", async () => {
    const encrypt = async (p: string) => `enc(${p})`;
    expect(await resolveSaveSentinelPassword({ ...REDIS_DEFAULTS, sentinelPassword: "new" }, encrypt)).toEqual({
      sentinel_password: "enc(new)",
    });
  });
  it("保存:无新明文时沿用既有密文", async () => {
    const encrypt = async (p: string) => `enc(${p})`;
    expect(await resolveSaveSentinelPassword({ ...REDIS_DEFAULTS, encryptedSentinelPassword: "ENC" }, encrypt)).toEqual(
      { sentinel_password: "ENC" }
    );
  });
  it("保存:都无时不写", async () => {
    const encrypt = async (p: string) => `enc(${p})`;
    expect(await resolveSaveSentinelPassword(REDIS_DEFAULTS, encrypt)).toEqual({});
  });
});
