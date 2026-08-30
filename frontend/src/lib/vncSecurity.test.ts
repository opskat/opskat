import { describe, expect, it } from "vitest";
import { securityPolicyForVNCEncryption } from "@/lib/vncSecurity";

const fullSession = [5, 13, 129, 133];
const nonFullSession = [1, 2, 6, 16, 19, 22, 30, 113, 130];

describe("securityPolicyForVNCEncryption", () => {
  it("keeps missing, empty, and server policy compatible with server order", () => {
    expect(securityPolicyForVNCEncryption()).toEqual([]);
    expect(securityPolicyForVNCEncryption("")).toEqual([]);
    expect(securityPolicyForVNCEncryption("server")).toEqual([]);
  });

  it("maps every configured policy to ordered noVNC preference groups", () => {
    expect(securityPolicyForVNCEncryption("always_maximum")).toEqual([[129, 133]]);
    expect(securityPolicyForVNCEncryption("always_on")).toEqual([fullSession]);
    expect(securityPolicyForVNCEncryption("prefer_on")).toEqual([fullSession, nonFullSession]);
    expect(securityPolicyForVNCEncryption("prefer_off")).toEqual([nonFullSession, fullSession]);
  });

  it("rejects unknown non-empty policy tokens", () => {
    expect(() => securityPolicyForVNCEncryption("downgrade")).toThrow(/downgrade/);
  });
});
