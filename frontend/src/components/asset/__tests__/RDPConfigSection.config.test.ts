import { describe, expect, it } from "vitest";
import {
  buildRDPConfig,
  parseRDPCredentialConfig,
  parseRDPConfig,
  RDP_DEFAULTS,
  type RDPFormState,
} from "../RDPConfigSection.config";
import { CONNECTION_DEFAULTS } from "../proxyConfig";

function form(overrides: Partial<RDPFormState> = {}): RDPFormState {
  return { ...RDP_DEFAULTS, host: "192.168.8.156", username: "win11", ...overrides };
}

describe("RDPConfigSection.config", () => {
  it("builds exact JSON for inline password config", () => {
    expect(buildRDPConfig(form({ domain: "LAB", clipboard: false }), { password: "enc" })).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":false,"domain":"LAB","password":"enc"}'
    );
  });

  it("builds exact JSON for managed credential config", () => {
    expect(buildRDPConfig(form(), { credential_id: 42 })).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":true,"credential_id":42}'
    );
  });

  it("defaults port and omits blank credentials", () => {
    expect(buildRDPConfig(form({ port: 0, domain: "  " }), {})).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":true}'
    );
  });

  it("builds proxy JSON in proxy mode with resolved password", () => {
    expect(
      buildRDPConfig(
        form({ connectionType: "proxy", proxyHost: "p.example", proxyPort: 1081, proxyUsername: "u" }),
        {},
        false,
        "encp"
      )
    ).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":true,"proxy":{"type":"socks5","host":"p.example","port":1081,"username":"u","password":"encp"}}'
    );
  });

  it("omits proxy outside proxy mode and tunnel outside jumphost mode", () => {
    const built = buildRDPConfig(form({ proxyHost: "p.example", sshTunnelId: 9 }), {});
    expect(built).not.toContain("proxy");
    expect(built).not.toContain("ssh_asset_id");
  });

  it("writes ssh_asset_id only for the test path (includeSshAssetId)", () => {
    const jumphost = form({ connectionType: "jumphost", sshTunnelId: 9 });
    expect(buildRDPConfig(jumphost, {}, true)).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":true,"ssh_asset_id":9}'
    );
    expect(buildRDPConfig(jumphost, {})).not.toContain("ssh_asset_id");
  });

  it("parses full config and preserves explicit clipboard false", () => {
    expect(
      parseRDPConfig(
        '{"host":"host","port":3390,"username":"user","domain":"DOMAIN","width":1024,"height":768,"clipboard":false}'
      )
    ).toEqual({
      host: "host",
      port: 3390,
      username: "user",
      domain: "DOMAIN",
      clipboard: false,
      ...CONNECTION_DEFAULTS,
    });
  });

  it("parses proxy config into proxy mode without echoing the password", () => {
    expect(
      parseRDPConfig(
        '{"host":"h","port":3389,"username":"u","clipboard":true,"proxy":{"type":"socks5","host":"p.example","port":1081,"username":"pu","password":"encp"}}'
      )
    ).toMatchObject({
      connectionType: "proxy",
      proxyHost: "p.example",
      proxyPort: 1081,
      proxyUsername: "pu",
      proxyPassword: "",
      encryptedProxyPassword: "encp",
    });
  });

  it("prefers the asset tunnel column over proxy when deriving connection type", () => {
    expect(
      parseRDPConfig('{"host":"h","port":3389,"username":"u","clipboard":true,"proxy":{"type":"socks5","host":"p","port":1080}}', 5)
    ).toMatchObject({ connectionType: "jumphost", sshTunnelId: 5 });
  });

  it("falls back to defaults for invalid config", () => {
    expect(parseRDPConfig("{")).toEqual(RDP_DEFAULTS);
  });

  it("parses credential fragments", () => {
    expect(parseRDPCredentialConfig('{"credential_id":7}')).toEqual({ credential_id: 7, password: undefined });
    expect(parseRDPCredentialConfig('{"password":"enc"}')).toEqual({ credential_id: undefined, password: "enc" });
    expect(parseRDPCredentialConfig("{")).toEqual({});
  });
});
