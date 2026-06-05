import { describe, it, expect, vi } from "vitest";
import {
  initCredentialFromConfig,
  resolveTestCredential,
  resolveSaveCredential,
  CREDENTIAL_DEFAULTS,
} from "@/components/asset/credentialConfig";

describe("initCredentialFromConfig (锁旧 load*Config credential 分支)", () => {
  it("credential_id 存在 → managed", () => {
    expect(initCredentialFromConfig({ credential_id: 7, password: "x" })).toEqual({
      password: "",
      encryptedPassword: "",
      passwordSource: "managed",
      passwordCredentialId: 7,
    });
  });
  it("无 credential_id → inline + 既有密文", () => {
    expect(initCredentialFromConfig({ password: "ENC" })).toEqual({
      password: "",
      encryptedPassword: "ENC",
      passwordSource: "inline",
      passwordCredentialId: 0,
    });
  });
  it("空 config → 默认", () => {
    expect(initCredentialFromConfig({})).toEqual(CREDENTIAL_DEFAULTS);
  });
});

describe("resolveTestCredential (锁旧 applyTestPasswordSource)", () => {
  it("managed + credId>0 → credential_id", () => {
    expect(
      resolveTestCredential({ password: "", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: 9 })
    ).toEqual({ credential_id: 9 });
  });
  it("inline 未改但有既有密文 → password=既有密文", () => {
    expect(
      resolveTestCredential({
        password: "",
        encryptedPassword: "ENC",
        passwordSource: "inline",
        passwordCredentialId: 0,
      })
    ).toEqual({ password: "ENC" });
  });
  it("inline 输入了明文 → 空片段(明文走 4th-arg)", () => {
    expect(
      resolveTestCredential({
        password: "plain",
        encryptedPassword: "",
        passwordSource: "inline",
        passwordCredentialId: 0,
      })
    ).toEqual({});
  });
  it("managed 但 credId=0 → 退回 inline 规则", () => {
    expect(
      resolveTestCredential({ password: "", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: 0 })
    ).toEqual({});
  });
});

describe("resolveSaveCredential (锁旧 save 分支 + encryptPasswordValue)", () => {
  it("managed + credId>0 → credential_id,不加密", async () => {
    const encrypt = vi.fn();
    expect(
      await resolveSaveCredential(
        { password: "p", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: 4 },
        encrypt
      )
    ).toEqual({ credential_id: 4 });
    expect(encrypt).not.toHaveBeenCalled();
  });
  it("inline 有明文 → 加密后 password", async () => {
    const encrypt = vi.fn(async (p: string) => `enc(${p})`);
    expect(
      await resolveSaveCredential(
        { password: "secret", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
        encrypt
      )
    ).toEqual({ password: "enc(secret)" });
  });
  it("inline 无明文但有既有密文 → 沿用既有密文", async () => {
    const encrypt = vi.fn();
    expect(
      await resolveSaveCredential(
        { password: "", encryptedPassword: "OLD", passwordSource: "inline", passwordCredentialId: 0 },
        encrypt
      )
    ).toEqual({ password: "OLD" });
    expect(encrypt).not.toHaveBeenCalled();
  });
  it("inline 无明文无密文 → 空片段(不写 password 键)", async () => {
    expect(
      await resolveSaveCredential(
        { password: "", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
        vi.fn()
      )
    ).toEqual({});
  });
  it("加密 reject → 透传(save 中止由上层处理)", async () => {
    await expect(
      resolveSaveCredential(
        { password: "x", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
        async () => {
          throw new Error("boom");
        }
      )
    ).rejects.toThrow("boom");
  });
});
