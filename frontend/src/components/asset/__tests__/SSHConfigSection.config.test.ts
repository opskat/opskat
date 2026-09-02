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

  describe("agent-auth", () => {
    const agentState = base({
      authType: "agent",
      agentSourceId: 7,
      agentKeyFingerprint: "SHA256:9ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmn",
    });

    it("来源+指纹 → agent_source_id + agent_key_fingerprint(位于 auth_type 后)", () => {
      expect(buildSSHConfig(agentState, NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"agent",' +
          '"agent_source_id":7,"agent_key_fingerprint":"SHA256:9ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmn"}'
      );
    });

    it("agent 与密码/密钥互斥:即使 state 残留 password/key 字段也不序列化", () => {
      const dirty = base({
        authType: "agent",
        agentSourceId: 7,
        agentKeyFingerprint: "SHA256:9ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmn",
        // 残留互斥字段:切换认证方式后不应再写入
        credentialId: 5,
        keySource: "file",
        selectedKeyPaths: ["/id_rsa"],
        privateKeyPassphrase: "PP",
        encryptedPrivateKeyPassphrase: "PPENC",
      });
      const json = buildSSHConfig(dirty, { ...NO_SECRETS, keyCredentialId: 5, passphrase: "PPENC" });
      expect(json).toContain("agent_source_id");
      expect(json).not.toContain("credential_id");
      expect(json).not.toContain("private_keys");
      expect(json).not.toContain("private_key_passphrase");
    });

    it("agent 未选来源或指纹 → 不写 agent 字段(留待 validate 拦截)", () => {
      expect(buildSSHConfig(base({ authType: "agent" }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"agent"}'
      );
    });

    it("password/key 切换离开 agent → 不写 agent 字段(互斥清除)", () => {
      const away = base({
        authType: "password",
        agentSourceId: 7,
        agentKeyFingerprint: "SHA256:9ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmn",
      });
      expect(buildSSHConfig(away, { ...NO_SECRETS, passwordCred: { credential_id: 3 } })).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","credential_id":3}'
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

  describe("keepalive 覆盖(0=跟随全局,不写入)", () => {
    it(">0 → 写 keepalive_interval_seconds(位于 proxy 之后)", () => {
      expect(buildSSHConfig(base({ keepAliveIntervalSeconds: 45 }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","keepalive_interval_seconds":45}'
      );
    });
    it("0 → 省略(跟随全局默认)", () => {
      expect(buildSSHConfig(base({ keepAliveIntervalSeconds: 0 }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
      );
    });
  });

  describe("restoreCwdOnReconnect 覆盖(false 不写入)", () => {
    it("true → 写 restore_cwd_on_reconnect(位于 keepalive 之后)", () => {
      expect(buildSSHConfig(base({ keepAliveIntervalSeconds: 45, restoreCwdOnReconnect: true }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","keepalive_interval_seconds":45,"restore_cwd_on_reconnect":true}'
      );
    });
    it("false → 省略", () => {
      expect(buildSSHConfig(base({ restoreCwdOnReconnect: false }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
      );
    });
  });

  describe("SSH Agent 转发(false 不写入)", () => {
    it("启用并选择来源 → 写 agent_forwarding + agent_forward_source_id", () => {
      expect(buildSSHConfig(base({ agentForwarding: true, agentForwardSourceId: 7 }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","agent_forwarding":true,"agent_forward_source_id":7}'
      );
    });

    it("关闭 → 不写转发字段", () => {
      expect(buildSSHConfig(base({ agentForwarding: false, agentForwardSourceId: 7 }), NO_SECRETS)).not.toContain(
        "agent_forward"
      );
    });
  });

  describe("startupCommand 启动命令(空值不写入)", () => {
    it("非空 → 写 startup_command", () => {
      expect(buildSSHConfig(base({ startupCommand: "cd /data" }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password","startup_command":"cd /data"}'
      );
    });
    it("空值 → 省略", () => {
      expect(buildSSHConfig(base({ startupCommand: "" }), NO_SECRETS)).toBe(
        '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}'
      );
    });
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

  it("keepalive_interval_seconds → keepAliveIntervalSeconds;缺省为 0", () => {
    expect(
      parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password","keepalive_interval_seconds":45}')
        .keepAliveIntervalSeconds
    ).toBe(45);
    expect(
      parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password"}').keepAliveIntervalSeconds
    ).toBe(0);
  });

  it("restore_cwd_on_reconnect → restoreCwdOnReconnect;缺省为 false", () => {
    expect(
      parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password","restore_cwd_on_reconnect":true}')
        .restoreCwdOnReconnect
    ).toBe(true);
    expect(parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password"}').restoreCwdOnReconnect).toBe(
      false
    );
  });

  it("agent_forwarding / agent_forward_source_id → 转发状态;缺省关闭", () => {
    const enabled = parseSSHConfig(
      '{"host":"h","port":22,"username":"u","auth_type":"password","agent_forwarding":true,"agent_forward_source_id":7}'
    );
    expect(enabled.agentForwarding).toBe(true);
    expect(enabled.agentForwardSourceId).toBe(7);

    const disabled = parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password"}');
    expect(disabled.agentForwarding).toBe(false);
    expect(disabled.agentForwardSourceId).toBe(0);
  });

  it("startup_command → startupCommand;缺省为空", () => {
    expect(
      parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password","startup_command":"cd /data"}')
        .startupCommand
    ).toBe("cd /data");
    expect(parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password"}').startupCommand).toBe("");
  });

  it("agent:agent_source_id/agent_key_fingerprint → agentSourceId/agentKeyFingerprint;残留互斥字段不进入 agent 态", () => {
    const s = parseSSHConfig(
      '{"host":"h","port":22,"username":"u","auth_type":"agent",' +
        '"agent_source_id":7,"agent_key_fingerprint":"SHA256:9ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmn"}'
    );
    expect(s.authType).toBe("agent");
    expect(s.agentSourceId).toBe(7);
    expect(s.agentKeyFingerprint).toBe("SHA256:9ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmn");
    expect(s.agentMissingFingerprint).toBe("");
    expect(s.credentialId).toBe(0);
    expect(s.selectedKeyPaths).toEqual([]);
  });

  it("非 agent:即便 config 残留 agent 字段也不回填", () => {
    const s = parseSSHConfig('{"host":"h","port":22,"username":"u","auth_type":"password","agent_source_id":7}');
    expect(s.authType).toBe("password");
    expect(s.agentSourceId).toBe(0);
    expect(s.agentKeyFingerprint).toBe("");
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
