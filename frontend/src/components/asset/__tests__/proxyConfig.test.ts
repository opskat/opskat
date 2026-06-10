import { describe, it, expect } from "vitest";
import {
  CONNECTION_DEFAULTS,
  parseConnectionFields,
  buildProxyJSON,
  resolveSaveProxyPassword,
  type ConnectionFormFields,
} from "../proxyConfig";

const proxyJSON = { type: "socks5", host: "p.example.com", port: 1081, username: "pu", password: "enc" };

describe("parseConnectionFields", () => {
  it("无隧道无代理派生 direct", () => {
    const f = parseConnectionFields(undefined, 0);
    expect(f).toEqual(CONNECTION_DEFAULTS);
  });

  it("有代理派生 proxy 并回填字段(密码入 encrypted,不回显明文)", () => {
    const f = parseConnectionFields(proxyJSON, 0);
    expect(f.connectionType).toBe("proxy");
    expect(f.proxyType).toBe("socks5");
    expect(f.proxyHost).toBe("p.example.com");
    expect(f.proxyPort).toBe(1081);
    expect(f.proxyUsername).toBe("pu");
    expect(f.proxyPassword).toBe("");
    expect(f.encryptedProxyPassword).toBe("enc");
  });

  it("隧道优先于代理", () => {
    const f = parseConnectionFields(proxyJSON, 7);
    expect(f.connectionType).toBe("jumphost");
    expect(f.sshTunnelId).toBe(7);
    // 代理字段仍回填,便于用户切回代理模式
    expect(f.proxyHost).toBe("p.example.com");
  });
});

describe("buildProxyJSON", () => {
  const base: ConnectionFormFields = {
    ...CONNECTION_DEFAULTS,
    connectionType: "proxy",
    proxyHost: "p.example.com",
    proxyPort: 1081,
    proxyUsername: "pu",
  };

  it("proxy 模式输出代理对象(键序固定)", () => {
    expect(JSON.stringify(buildProxyJSON(base, "cipher"))).toBe(
      '{"type":"socks5","host":"p.example.com","port":1081,"username":"pu","password":"cipher"}'
    );
  });

  it("非 proxy 模式不输出", () => {
    expect(buildProxyJSON({ ...base, connectionType: "jumphost" }, "cipher")).toBeUndefined();
    expect(buildProxyJSON({ ...base, connectionType: "direct" }, "cipher")).toBeUndefined();
  });

  it("缺 host 不输出", () => {
    expect(buildProxyJSON({ ...base, proxyHost: "" }, "cipher")).toBeUndefined();
  });

  it("空用户名/密码省略字段", () => {
    expect(JSON.stringify(buildProxyJSON({ ...base, proxyUsername: "" }, ""))).toBe(
      '{"type":"socks5","host":"p.example.com","port":1081}'
    );
  });
});

describe("resolveSaveProxyPassword", () => {
  const encrypt = async (s: string) => `enc(${s})`;

  it("明文优先加密", async () => {
    const f = { ...CONNECTION_DEFAULTS, proxyPassword: "plain", encryptedProxyPassword: "old" };
    expect(await resolveSaveProxyPassword(f, encrypt)).toBe("enc(plain)");
  });

  it("无明文沿用既有密文", async () => {
    const f = { ...CONNECTION_DEFAULTS, encryptedProxyPassword: "old" };
    expect(await resolveSaveProxyPassword(f, encrypt)).toBe("old");
  });
});
