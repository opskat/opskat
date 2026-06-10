import { describe, it, expect } from "vitest";
import {
  buildEtcdConfig,
  parseEtcdConfig,
  parseEtcdEndpoints,
  ETCD_DEFAULTS,
  type EtcdFormState,
} from "@/components/asset/EtcdConfigSection.config";
import { CONNECTION_DEFAULTS } from "@/components/asset/proxyConfig";

const FULL: EtcdFormState = {
  ...CONNECTION_DEFAULTS,
  endpoints: "10.0.0.1:2379\n10.0.0.2:2379",
  username: "admin",
  tls: true,
  tlsInsecure: true,
  tlsServerName: "etcd.x",
  tlsCAFile: "/ca.pem",
  tlsCertFile: "/c.crt",
  tlsKeyFile: "/c.key",
  dialTimeoutSeconds: 5,
  commandTimeoutSeconds: 10,
  connectionType: "jumphost",
  sshTunnelId: 3,
};

const PROXY_FULL: EtcdFormState = {
  ...FULL,
  connectionType: "proxy",
  sshTunnelId: 0,
  proxyHost: "p.example.com",
  proxyPort: 1081,
  proxyUsername: "pu",
};

describe("parseEtcdEndpoints (锁旧 split(/[\\n,;]+/))", () => {
  it("混合换行/逗号/分号 + trim + 去空", () => {
    expect(parseEtcdEndpoints(" a:2379 \n b:2379 , c:2379 ; ")).toEqual(["a:2379", "b:2379", "c:2379"]);
  });
  it("全空 → 空数组", () => {
    expect(parseEtcdEndpoints("  \n ; , ")).toEqual([]);
  });
});

describe("buildEtcdConfig (锁旧 save 序:endpoints→username→credential→tls…→timeouts→ssh_asset_id)", () => {
  it("全字段 + 既加密 password", () => {
    expect(buildEtcdConfig(FULL, { password: "ENC" })).toBe(
      '{"endpoints":["10.0.0.1:2379","10.0.0.2:2379"],"username":"admin","password":"ENC",' +
        '"tls":true,"tls_insecure":true,"tls_server_name":"etcd.x","tls_ca_file":"/ca.pem",' +
        '"tls_cert_file":"/c.crt","tls_key_file":"/c.key","dial_timeout_seconds":5,' +
        '"command_timeout_seconds":10,"ssh_asset_id":3}'
    );
  });
  it("managed 凭据 → credential_id 紧跟 username", () => {
    expect(buildEtcdConfig(FULL, { credential_id: 7 })).toContain('"username":"admin","credential_id":7,"tls":true');
  });
  it("最小态(仅端点,默认超时仍写)", () => {
    expect(buildEtcdConfig({ ...ETCD_DEFAULTS, endpoints: "x:2379" }, {})).toBe(
      '{"endpoints":["x:2379"],"dial_timeout_seconds":5,"command_timeout_seconds":10}'
    );
  });
  it("tls=false 时省略全部 tls_* 子键", () => {
    const s = { ...FULL, tls: false };
    const json = buildEtcdConfig(s, {});
    expect(json).not.toContain("tls_insecure");
    expect(json).not.toContain("tls_server_name");
    expect(json).not.toContain('"tls":');
  });
  it("空片段不写 password / credential_id 键", () => {
    const json = buildEtcdConfig({ ...ETCD_DEFAULTS, endpoints: "x:2379" }, {});
    expect(json).not.toContain("password");
    expect(json).not.toContain("credential_id");
  });
  it("超时为 0 时省略对应键", () => {
    const json = buildEtcdConfig(
      { ...ETCD_DEFAULTS, endpoints: "x:2379", dialTimeoutSeconds: 0, commandTimeoutSeconds: 0 },
      {}
    );
    expect(json).toBe('{"endpoints":["x:2379"]}');
  });

  it("proxy 模式写 proxy 不写 ssh_asset_id(键序:timeouts 后尾部)", () => {
    expect(buildEtcdConfig(PROXY_FULL, { password: "ENC" }, "PROXYENC")).toBe(
      '{"endpoints":["10.0.0.1:2379","10.0.0.2:2379"],"username":"admin","password":"ENC",' +
        '"tls":true,"tls_insecure":true,"tls_server_name":"etcd.x","tls_ca_file":"/ca.pem",' +
        '"tls_cert_file":"/c.crt","tls_key_file":"/c.key","dial_timeout_seconds":5,' +
        '"command_timeout_seconds":10,' +
        '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"}}'
    );
  });

  it("jumphost 模式不写 proxy(互斥,即便 proxy 字段有值)", () => {
    const json = buildEtcdConfig({ ...PROXY_FULL, connectionType: "jumphost", sshTunnelId: 3 }, {}, "PROXYENC");
    expect(json).toContain('"ssh_asset_id":3');
    expect(json).not.toContain('"proxy"');
  });

  it("direct 模式不写 proxy 也不写 ssh_asset_id", () => {
    const json = buildEtcdConfig({ ...PROXY_FULL, connectionType: "direct" }, {}, "PROXYENC");
    expect(json).not.toContain('"proxy"');
    expect(json).not.toContain("ssh_asset_id");
  });
});

describe("parseEtcdConfig (锁旧 loadEtcdConfig 非凭据字段)", () => {
  it("全字段回填(ssh_asset_id 来自 config,派生 connectionType=jumphost)", () => {
    expect(
      parseEtcdConfig(
        '{"endpoints":["a:2379","b:2379"],"username":"u","tls":true,"tls_insecure":true,' +
          '"tls_server_name":"sn","tls_ca_file":"/ca","tls_cert_file":"/cc","tls_key_file":"/ck",' +
          '"dial_timeout_seconds":8,"command_timeout_seconds":20,"ssh_asset_id":5}'
      )
    ).toEqual({
      ...CONNECTION_DEFAULTS,
      endpoints: "a:2379\nb:2379",
      username: "u",
      tls: true,
      tlsInsecure: true,
      tlsServerName: "sn",
      tlsCAFile: "/ca",
      tlsCertFile: "/cc",
      tlsKeyFile: "/ck",
      dialTimeoutSeconds: 8,
      commandTimeoutSeconds: 20,
      connectionType: "jumphost",
      sshTunnelId: 5,
    });
  });
  it("缺字段用默认", () => {
    expect(parseEtcdConfig("{}")).toEqual(ETCD_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseEtcdConfig("nope")).toEqual(ETCD_DEFAULTS);
  });

  it("带 proxy 回填并派生 connectionType=proxy(密码入 encrypted)", () => {
    const s = parseEtcdConfig(
      '{"endpoints":["a:2379"],' +
        '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"}}'
    );
    expect(s.connectionType).toBe("proxy");
    expect(s.proxyHost).toBe("p.example.com");
    expect(s.proxyPort).toBe(1081);
    expect(s.proxyUsername).toBe("pu");
    expect(s.proxyPassword).toBe("");
    expect(s.encryptedProxyPassword).toBe("PROXYENC");
  });

  it("assetTunnelId 入参优先派生 jumphost(镜像 asset.sshTunnelId 优先)", () => {
    const s = parseEtcdConfig('{"endpoints":["a:2379"],"proxy":{"type":"socks5","host":"p","port":1080}}', 6);
    expect(s.connectionType).toBe("jumphost");
    expect(s.sshTunnelId).toBe(6);
  });

  it("parse→build 往返(proxy,密文沿用)", () => {
    const original =
      '{"endpoints":["a:2379"],"username":"u","password":"OLD",' +
      '"dial_timeout_seconds":5,"command_timeout_seconds":10,' +
      '"proxy":{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"PROXYENC"}}';
    const state = parseEtcdConfig(original);
    expect(buildEtcdConfig(state, { password: "OLD" }, state.encryptedProxyPassword)).toBe(original);
  });
});
