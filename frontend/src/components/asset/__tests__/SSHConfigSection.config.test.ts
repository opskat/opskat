import { describe, it, expect } from "vitest";
import {
  buildSSHConfig,
  parseSSHConfig,
  SSH_DEFAULTS,
  type SSHBuildOptions,
  type SSHFormState,
} from "@/components/asset/SSHConfigSection.config";

const NO_SECRETS: SSHBuildOptions = {
  passwordCred: {},
  keyCredentialId: 0,
  passphrase: "",
  proxyPassword: "",
  includeJumpHost: false,
};

const base = (over: Partial<SSHFormState>): SSHFormState => ({ ...SSH_DEFAULTS, host: "1.2.3.4", ...over });

describe("buildSSHConfig (锁旧 save/test 序:host→port→username→auth_type→凭据/密钥→jump_host_id→proxy)", () => {
  describe("password-auth", () => {
    it("managed → credential_id 紧跟 auth_type", () => {
      expect(
        buildSSHConfig(base({ authType: "password" }), { ...NO_SECRETS, passwordCred: { credential_id: 7 } })
      ).toBe('{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","credential_id":7}');
    });
    it("inline 既有/新加密密文 → password", () => {
      expect(buildSSHConfig(base({ authType: "password" }), { ...NO_SECRETS, passwordCred: { password: "ENC" } })).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","password":"ENC"}'
      );
    });
    it("空凭据片段不写 password / credential_id", () => {
      expect(buildSSHConfig(base({ authType: "password" }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
      );
    });
  });

  describe("key-auth", () => {
    it("managed ssh_key 凭据 → credential_id(来自 keyCredentialId)", () => {
      expect(
        buildSSHConfig(base({ authType: "key", keySource: "managed" }), { ...NO_SECRETS, keyCredentialId: 5 })
      ).toBe('{"host":"1.2.3.4","port":22,"username":"root","auth_type":"key","credential_id":5}');
    });
    it("file + passphrase → private_keys + private_key_passphrase", () => {
      expect(
        buildSSHConfig(base({ authType: "key", keySource: "file", selectedKeyPaths: ["/a", "/b"] }), {
          ...NO_SECRETS,
          passphrase: "PP",
        })
      ).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"key",' +
          '"private_keys":["/a","/b"],"private_key_passphrase":"PP"}'
      );
    });
    it("file 无 passphrase → 省略 private_key_passphrase", () => {
      expect(buildSSHConfig(base({ authType: "key", keySource: "file", selectedKeyPaths: ["/a"] }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"key","private_keys":["/a"]}'
      );
    });
    it("file 无选中文件 → 不写 private_keys", () => {
      expect(buildSSHConfig(base({ authType: "key", keySource: "file", selectedKeyPaths: [] }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"key"}'
      );
    });
    it("managed 但 keyCredentialId=0 → 不写 credential_id", () => {
      expect(buildSSHConfig(base({ authType: "key", keySource: "managed" }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"key"}'
      );
    });
  });

  describe("proxy", () => {
    it("proxy + 密码 + username", () => {
      expect(
        buildSSHConfig(
          base({
            authType: "password",
            connectionType: "proxy",
            proxyType: "socks5",
            proxyHost: "127.0.0.1",
            proxyPort: 1080,
            proxyUsername: "pu",
          }),
          { ...NO_SECRETS, proxyPassword: "PENC" }
        )
      ).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password",' +
          '"proxy":{"type":"socks5","host":"127.0.0.1","port":1080,"username":"pu","password":"PENC"}}'
      );
    });
    it("proxy 无 username/password → 省略(undefined 不序列化)", () => {
      expect(buildSSHConfig(base({ connectionType: "proxy", proxyHost: "h", proxyPort: 1080 }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","proxy":{"type":"socks5","host":"h","port":1080}}'
      );
    });
    it("connectionType=proxy 但 proxyHost 空 → 不写 proxy", () => {
      expect(buildSSHConfig(base({ connectionType: "proxy", proxyHost: "" }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
      );
    });
  });

  describe("jumphost(save 省略 jump_host_id;test 写入)", () => {
    const jh = base({ connectionType: "jumphost", sshTunnelId: 42 });
    it("includeJumpHost=false(save)→ 不写 jump_host_id", () => {
      expect(buildSSHConfig(jh, { ...NO_SECRETS, includeJumpHost: false })).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
      );
    });
    it("includeJumpHost=true(test)→ jump_host_id 在 proxy 之前", () => {
      expect(buildSSHConfig(jh, { ...NO_SECRETS, includeJumpHost: true })).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","jump_host_id":42}'
      );
    });
    it("includeJumpHost=true 但 sshTunnelId=0 → 不写", () => {
      expect(
        buildSSHConfig(base({ connectionType: "jumphost", sshTunnelId: 0 }), { ...NO_SECRETS, includeJumpHost: true })
      ).toBe('{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}');
    });
    it("includeJumpHost=true 但 connectionType≠jumphost → 不写", () => {
      expect(
        buildSSHConfig(base({ connectionType: "direct", sshTunnelId: 42 }), { ...NO_SECRETS, includeJumpHost: true })
      ).toBe('{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}');
    });
  });

  it("direct + minimal:仅 host/port/username/auth_type", () => {
    expect(buildSSHConfig(base({}), NO_SECRETS)).toBe(
      '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
    );
  });
});

describe("parseSSHConfig (镜像旧 loadSSHConfig)", () => {
  it("password managed:credential_id → keySource managed,connectionType direct", () => {
    const s = parseSSHConfig('{"host":"h","port":2222,"username":"u","auth_type":"password","credential_id":3}');
    expect(s).toEqual({
      ...SSH_DEFAULTS,
      host: "h",
      port: 2222,
      username: "u",
      authType: "password",
      keySource: "managed",
      // password 凭据不在 SSHFormState,credentialId 仅 key-auth 取
      credentialId: 0,
    });
  });

  it("key-auth managed:credentialId 取自 config", () => {
    const s = parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"key","credential_id":9}');
    expect(s.authType).toBe("key");
    expect(s.credentialId).toBe(9);
    expect(s.keySource).toBe("managed");
  });

  it("key-auth file:private_keys → keySource file + 既有 passphrase 密文保留,明文不回显", () => {
    const s = parseSSHConfig(
      '{"host":"h","port":22,"username":"u","auth_type":"key","private_keys":["/k"],"private_key_passphrase":"PPENC"}'
    );
    expect(s.keySource).toBe("file");
    expect(s.selectedKeyPaths).toEqual(["/k"]);
    expect(s.privateKeyPassphrase).toBe("");
    expect(s.encryptedPrivateKeyPassphrase).toBe("PPENC");
    expect(s.credentialId).toBe(0);
  });

  it("proxy:connectionType proxy + 字段回填 + 既有密码密文保留", () => {
    const s = parseSSHConfig(
      '{"host":"h","port":22,"username":"u","auth_type":"password",' +
        '"proxy":{"type":"socks5","host":"px","port":1080,"username":"pu","password":"PENC"}}'
    );
    expect(s.connectionType).toBe("proxy");
    expect(s.proxyHost).toBe("px");
    expect(s.proxyPort).toBe(1080);
    expect(s.proxyUsername).toBe("pu");
    expect(s.encryptedProxyPassword).toBe("PENC");
    expect(s.proxyPassword).toBe("");
  });

  it("jumphost:config.jump_host_id → connectionType jumphost,sshTunnelId 取该值", () => {
    const s = parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password","jump_host_id":11}');
    expect(s.connectionType).toBe("jumphost");
    expect(s.sshTunnelId).toBe(11);
  });

  it("asset 顶层 tunnelId 优先于 config.jump_host_id 派生 connectionType", () => {
    const s = parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password"}', 77);
    expect(s.connectionType).toBe("jumphost");
    expect(s.sshTunnelId).toBe(77);
  });

  it("缺字段用默认", () => {
    expect(parseSSHConfig("{}")).toEqual(SSH_DEFAULTS);
  });

  it("非法 JSON 回退默认", () => {
    expect(parseSSHConfig("nope")).toEqual(SSH_DEFAULTS);
  });

  it("round-trip:parse → build(save)还原(无凭据/无 passphrase 时键序一致)", () => {
    const json = '{"host":"h","port":2200,"username":"u","auth_type":"key","private_keys":["/k"]}';
    const s = parseSSHConfig(json);
    expect(
      buildSSHConfig(s, {
        passwordCred: {},
        keyCredentialId: 0,
        passphrase: "",
        proxyPassword: "",
        includeJumpHost: false,
      })
    ).toBe(json);
  });
});
