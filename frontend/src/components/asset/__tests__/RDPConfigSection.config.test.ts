import { describe, expect, it } from "vitest";
import {
  buildRDPConfig,
  parseRDPCredentialConfig,
  parseRDPConfig,
  RDP_DEFAULTS,
  type RDPFormState,
} from "../RDPConfigSection.config";

function form(overrides: Partial<RDPFormState> = {}): RDPFormState {
  return { ...RDP_DEFAULTS, host: "192.168.8.156", username: "win11", ...overrides };
}

describe("RDPConfigSection.config", () => {
  it("builds exact JSON for inline password config", () => {
    expect(buildRDPConfig(form({ domain: "LAB", width: 1440, height: 900, clipboard: false }), { password: "enc" })).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":false,"domain":"LAB","width":1440,"height":900,"password":"enc"}'
    );
  });

  it("builds exact JSON for managed credential config", () => {
    expect(buildRDPConfig(form(), { credential_id: 42 })).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":true,"width":1280,"height":720,"credential_id":42}'
    );
  });

  it("defaults port and omits blank credentials", () => {
    expect(buildRDPConfig(form({ port: 0, domain: "  " }), {})).toBe(
      '{"host":"192.168.8.156","port":3389,"username":"win11","clipboard":true,"width":1280,"height":720}'
    );
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
      width: 1024,
      height: 768,
      clipboard: false,
    });
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
