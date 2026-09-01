import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent, { PointerEventsCheckLevel } from "@testing-library/user-event";
import { createRef } from "react";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity, credential_entity, system, ssh_agent_svc } from "../../../../wailsjs/go/models";

// 托管凭据按类型注入:autofill 用例往这里塞 password / ssh_key 凭据,
// ref-契约用例保持默认空数组(与原 mock 行为一致)。
let pwCreds: credential_entity.Credential[] = [];
let keyCreds: credential_entity.Credential[] = [];
// SSH Agent 表单:可注入来源列表 / 检查结果 / 检查失败。
let agentSources: system.AgentSourceSummary[] = [];
let agentIdentities: ssh_agent_svc.IdentitySummary[] = [];
let agentInspectError: string | null = null;

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: (type: string) =>
    Promise.resolve(type === "ssh_key" ? keyCreds : type === "password" ? pwCreds : []),
  GetAssetPassword: () => Promise.resolve(""),
  GetSSHConnectionSettings: () => Promise.resolve({ keepAliveIntervalSeconds: 30 }),
  ListAgentSources: () => Promise.resolve(agentSources),
  InspectAgentSource: (id: number) =>
    agentInspectError
      ? Promise.reject(new Error(agentInspectError))
      : Promise.resolve({ source_id: id, usages: 0, identities: agentIdentities }),
}));

vi.mock("../../../../wailsjs/go/ssh/SSH", () => ({
  ListLocalSSHKeys: () => Promise.resolve([]),
  SelectSSHKeyFile: () => Promise.resolve(null),
}));

beforeEach(() => {
  pwCreds = [];
  keyCreds = [];
  agentSources = [];
  agentIdentities = [];
  agentInspectError = null;
});

function makeCred(id: number, username: string, type = "password"): credential_entity.Credential {
  return { id, name: `cred-${id}`, username, type, keyType: "ed25519" } as credential_entity.Credential;
}

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("SSHConfigSection ref 契约", () => {
  it("创建态(无 host):上报 canSave/canTest=false + asset.formMissingHost", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingHost",
    });
  });

  it("编辑态(有 host):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"1.2.3.4","port":22,"username":"root","auth_type":"password"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("password-auth inline 既有密文:buildConfig 沿用密文;buildTestConfig 同形,password 空(4th arg)", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"u","auth_type":"password","password":"OLDENC"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON: '{"host":"h","port":22,"username":"u","auth_type":"password","password":"OLDENC"}',
      sshTunnelId: 0,
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({
      assetType: "ssh",
      configJSON: '{"host":"h","port":22,"username":"u","auth_type":"password","password":"OLDENC"}',
      password: "",
    });
  });

  it("key-auth file:buildConfig 加密 passphrase;buildTestConfig 用既有明文密文(不加密)", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config:
        '{"host":"h","port":22,"username":"u","auth_type":"key",' +
        '"private_keys":["/id_rsa"],"private_key_passphrase":"PPENC"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    // 用户未输入新 passphrase → save 沿用既有密文(不重新加密),test 也沿用既有密文。
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"key","private_keys":["/id_rsa"],"private_key_passphrase":"PPENC"}'
    );
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"key","private_keys":["/id_rsa"],"private_key_passphrase":"PPENC"}'
    );
  });

  it("key-auth managed credential 切到 password-auth 时不复用 ssh_key credential_id", async () => {
    const u = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"u","auth_type":"key","credential_id":9}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    await u.click(screen.getByTestId("ssh-auth-type-option-password"));

    await waitFor(async () => {
      const built = await ref.current!.buildConfig(ctx);
      expect(built.configJSON).toBe('{"host":"h","port":22,"username":"u","auth_type":"password"}');
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).toBe('{"host":"h","port":22,"username":"u","auth_type":"password"}');
  });

  it("proxy:buildConfig 加密 proxy 密码(沿用既有密文);buildTestConfig 仅明文(无既有密文回退)", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config:
        '{"host":"h","port":22,"username":"u","auth_type":"password",' +
        '"proxy":{"type":"socks5","host":"px","port":1080,"username":"pu","password":"PXENC"}}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    // 未改 proxy 密码 → save 沿用既有密文;test 无明文 → proxy.password undefined(省略键)。
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"password",' +
        '"proxy":{"type":"socks5","host":"px","port":1080,"username":"pu","password":"PXENC"},' +
        '"proxy_chain":{"layers":[{"id":"legacy-socks5-proxy","name":"SOCKS5 Proxy","enabled":true,"type":"socks5","order":1,"host":"px","port":1080,"username":"pu","password":"PXENC"}]}}'
    );
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"password",' +
        '"proxy":{"type":"socks5","host":"px","port":1080,"username":"pu"},' +
        '"proxy_chain":{"layers":[{"id":"legacy-socks5-proxy","name":"SOCKS5 Proxy","enabled":true,"type":"socks5","order":1,"host":"px","port":1080,"username":"pu","password":"PXENC"}]}}'
    );
  });

  it("jumphost:buildConfig sshTunnelId 置位 + config 无 jump_host_id;buildTestConfig config 含 jump_host_id", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      sshTunnelId: 42,
      Config: '{"host":"h","port":22,"username":"u","auth_type":"password"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.sshTunnelId).toBe(42);
    expect(built.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"password",' +
        '"proxy_chain":{"layers":[{"id":"legacy-ssh-42","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":42}]}}'
    );
    expect(built.configJSON).not.toContain("jump_host_id");
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"password","jump_host_id":42,' +
        '"proxy_chain":{"layers":[{"id":"legacy-ssh-42","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":42}]}}'
    );
  });
});

describe("SSHConfigSection 托管凭据→用户名自动填充", () => {
  // Radix Select 把 SelectValue 渲染成 pointer-events:none 的 <span>,
  // userEvent 必须先跳过 pointer-events 检查才能点开 trigger。
  const user = () => userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });

  // 经 ref.buildConfig 序列化后观察 username:buildSSHConfig 恒序列化 username 字段。
  async function builtUsername(ref: React.RefObject<AssetFormHandle | null>): Promise<string> {
    const built = await ref.current!.buildConfig(ctx);
    return (JSON.parse(built.configJSON) as { username: string }).username;
  }

  it("password-auth:选中带 username 的托管密码凭据 → username 自动填为 alice", async () => {
    pwCreds = [makeCred(1, "alice"), makeCred(2, "")];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"","auth_type":"password"}',
    });
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    // 切到 managed 来源(初始 inline),等托管选项异步加载出现后再选。
    await u.click(screen.getByTestId("password-source-managed"));
    await u.click(await screen.findByText("asset.selectPasswordPlaceholder"));
    await u.click(await screen.findByRole("option", { name: "cred-1 (alice)" }));

    expect(await builtUsername(ref)).toBe("alice");
  });

  it("password-auth:选中 username 为空的托管密码凭据 → username 不变(保留原值)", async () => {
    pwCreds = [makeCred(2, "")];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"preexisting","auth_type":"password"}',
    });
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    await u.click(screen.getByTestId("password-source-managed"));
    await u.click(await screen.findByText("asset.selectPasswordPlaceholder"));
    await u.click(await screen.findByRole("option", { name: "cred-2" }));

    expect(await builtUsername(ref)).toBe("preexisting");
  });

  it("key-auth:选中带 username 的托管 SSH key → username 自动填为 alice", async () => {
    keyCreds = [makeCred(10, "alice", "ssh_key"), makeCred(11, "", "ssh_key")];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    // editAsset 直接进 key-auth + managed(keySource 默认 managed),托管 key 异步加载。
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"","auth_type":"key"}',
    });
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    await u.click(await screen.findByText("asset.selectKeyPlaceholder"));
    await u.click(await screen.findByRole("option", { name: /cred-10 \(alice\) \(ED25519\)/ }));

    expect(await builtUsername(ref)).toBe("alice");
  });

  it("key-auth:选中 username 为空的托管 SSH key → username 不变(保留原值)", async () => {
    keyCreds = [makeCred(11, "", "ssh_key")];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"preexisting","auth_type":"key"}',
    });
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    await u.click(await screen.findByText("asset.selectKeyPlaceholder"));
    await u.click(await screen.findByRole("option", { name: /cred-11 \(ED25519\)/ }));

    expect(await builtUsername(ref)).toBe("preexisting");
    // 防御:确保选项确实被点中(避免误以为"没填"实则没点到)。
    await waitFor(() => expect(screen.queryByRole("option")).toBeNull());
  });
});

describe("SSHConfigSection 高级设置", () => {
  // GetSSHConnectionSettings mock 返回全局默认 30;保活输入在「高级」标签,需先切换。
  const openAdvanced = async (u: ReturnType<typeof userEvent.setup>) =>
    u.click(await screen.findByTestId("config-tab-advanced"));

  it("启动命令在高级页编辑并写入配置", async () => {
    const u = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);
    await openAdvanced(u);

    const input = await screen.findByTestId("ssh-startup-command-input");
    await u.type(input, "cd /data");
    await u.keyboard("{Enter}");
    await u.type(input, "docker compose ps");

    const built = await ref.current!.buildConfig(ctx);
    expect((JSON.parse(built.configJSON) as { startup_command?: string }).startup_command).toBe(
      "cd /data\ndocker compose ps"
    );
  });

  it("新建:保活输入预填全局默认(30),未改动 → buildConfig 写 keepalive_interval_seconds:30", async () => {
    const u = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);
    await openAdvanced(u);
    const input = await screen.findByTestId("ssh-keepalive-input");
    await waitFor(() => expect(input).toHaveValue(30));
    const built = await ref.current!.buildConfig(ctx);
    expect((JSON.parse(built.configJSON) as { keepalive_interval_seconds?: number }).keepalive_interval_seconds).toBe(
      30
    );
  });

  it("新建:改保活为 45 → buildConfig 写 keepalive_interval_seconds:45(固定覆盖)", async () => {
    const u = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);
    await openAdvanced(u);
    const input = await screen.findByTestId("ssh-keepalive-input");
    await waitFor(() => expect(input).toHaveValue(30));
    await u.clear(input);
    await u.type(input, "45");
    await u.tab();
    const built = await ref.current!.buildConfig(ctx);
    expect((JSON.parse(built.configJSON) as { keepalive_interval_seconds?: number }).keepalive_interval_seconds).toBe(
      45
    );
  });

  it("新建:清空预填 → 回落跟随全局(输入框空 + buildConfig 不写 keepalive)", async () => {
    const u = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);
    await openAdvanced(u);
    const input = await screen.findByTestId("ssh-keepalive-input");
    await waitFor(() => expect(input).toHaveValue(30));
    await u.clear(input);
    await u.tab();
    expect(input).toHaveValue(null);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).not.toContain("keepalive_interval_seconds");
  });

  it("编辑态:已存保活 45 → 输入框显示 45(不受新建预填影响)", async () => {
    const u = userEvent.setup();
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"u","auth_type":"password","keepalive_interval_seconds":45}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await openAdvanced(u);
    const input = await screen.findByTestId("ssh-keepalive-input");
    await waitFor(() => expect(input).toHaveValue(45));
  });

  it("编辑态:未设保活(0) → 输入框留空(不预填全局)", async () => {
    const u = userEvent.setup();
    const editAsset = new asset_entity.Asset({
      Type: "ssh",
      Config: '{"host":"h","port":22,"username":"u","auth_type":"password"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await openAdvanced(u);
    const input = await screen.findByTestId("ssh-keepalive-input");
    expect(input).toHaveValue(null);
  });
});

describe("SSHConfigSection Agent 认证", () => {
  const user = () => userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  const FP_SAVED = "SHA256:SAVEDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
  const FP_AVAIL = "SHA256:AVAILABLEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
  const FP_OTHER = "SHA256:OTHERBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB";

  function source(id: number, name: string): system.AgentSourceSummary {
    return { id, name, endpoint_type: "unix_socket" } as system.AgentSourceSummary;
  }
  function identity(fp: string, comment = "work key", type = "ssh-ed25519"): ssh_agent_svc.IdentitySummary {
    return { fingerprint: fp, type, comment, usages: 0 } as ssh_agent_svc.IdentitySummary;
  }
  const agentAsset = (fp: string) =>
    new asset_entity.Asset({
      Type: "ssh",
      Config: `{"host":"h","port":22,"username":"u","auth_type":"agent","agent_source_id":1,"agent_key_fingerprint":"${fp}"}`,
    });

  it("认证选择器包含 Agent 选项", async () => {
    render(<SSHConfigSection ref={createRef()} ctx={ctx} onValidityChange={() => {}} />);
    expect(screen.getByTestId("ssh-auth-type-option-agent")).toBeInTheDocument();
  });

  it("新建:即使只有一个来源也不自动选择来源/密钥,保存被禁(先选来源)", async () => {
    agentSources = [source(1, "System Agent")];
    agentIdentities = [identity(FP_AVAIL)];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    const onValidity = vi.fn();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);

    await u.click(screen.getByTestId("ssh-auth-type-option-agent"));
    await u.type(screen.getByTestId("ssh-host-input"), "1.2.3.4");
    // 来源列表加载后仍显示占位符(不自动选唯一的来源)。
    const trigger = await screen.findByTestId("ssh-agent-source-trigger");
    expect(within(trigger).queryByText("System Agent")).not.toBeInTheDocument();
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.agentSourceRequired",
    });
  });

  it("选择来源后加载身份;密钥显示指纹/类型/清理备注;不自动选唯一的身份;显式选择后可保存", async () => {
    agentSources = [source(1, "System Agent")];
    agentIdentities = [identity(FP_AVAIL, "dev laptop key", "ssh-rsa")];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    const onValidity = vi.fn();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);

    await u.click(screen.getByTestId("ssh-auth-type-option-agent"));
    await u.type(screen.getByTestId("ssh-host-input"), "1.2.3.4");
    await u.click(await screen.findByTestId("ssh-agent-source-trigger"));
    await u.click(await screen.findByRole("option", { name: "System Agent" }));

    // 身份加载完成后,触发仍显示占位符 —— 不自动选择唯一身份。
    const keyTrigger = await screen.findByTestId("ssh-agent-key-trigger");
    expect(within(keyTrigger).queryByText(FP_AVAIL)).not.toBeInTheDocument();
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.agentKeyRequired",
    });

    // 展开身份列表:选项展示 指纹/类型/清理备注。
    await u.click(keyTrigger);
    const keyOption = await screen.findByRole("option", { name: new RegExp(FP_AVAIL) });
    expect(keyOption.textContent).toContain("ssh-rsa");
    expect(keyOption.textContent).toContain("dev laptop key");

    // 显式选择身份 → 可保存,config 写入来源+指纹。
    await u.click(keyOption);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toMatchObject({
      auth_type: "agent",
      agent_source_id: 1,
      agent_key_fingerprint: FP_AVAIL,
    });
  });

  it("修改来源清除尚未保存的密钥选择", async () => {
    agentSources = [source(1, "Agent A"), source(2, "Agent B")];
    agentIdentities = [identity(FP_AVAIL)];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);

    await u.click(screen.getByTestId("ssh-auth-type-option-agent"));
    await u.click(await screen.findByTestId("ssh-agent-source-trigger"));
    await u.click(await screen.findByRole("option", { name: "Agent A" }));
    // 选一把身份。
    await u.click(await screen.findByTestId("ssh-agent-key-trigger"));
    await u.click(await screen.findByRole("option", { name: new RegExp(FP_AVAIL) }));
    let built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON).agent_key_fingerprint).toBe(FP_AVAIL);

    // 切到 Agent B → 身份选择被清空。
    await u.click(await screen.findByTestId("ssh-agent-source-trigger"));
    await u.click(await screen.findByRole("option", { name: "Agent B" }));
    await waitFor(async () => {
      built = await ref.current!.buildConfig(ctx);
      expect(JSON.parse(built.configJSON).agent_key_fingerprint).toBeUndefined();
    });
  });

  it("编辑:已存指纹当前缺失 → 单独只读'当前不可用'展示 + 禁止保存;显式选可用身份后恢复可保存", async () => {
    agentSources = [source(1, "System Agent")];
    agentIdentities = [identity(FP_OTHER, "other key")];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    const onValidity = vi.fn();
    render(<SSHConfigSection ref={ref} editAsset={agentAsset(FP_SAVED)} ctx={ctx} onValidityChange={onValidity} />);

    // 只读"当前不可用"行展示已存指纹,且不进入可选身份列表。
    const missingRow = await screen.findByTestId("ssh-agent-key-unavailable");
    expect(within(missingRow).getByText(FP_SAVED)).toBeInTheDocument();
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.agentKeyUnavailable",
    });

    // 显式选择当前可用身份 → 缺失态解除,保存恢复。
    await u.click(await screen.findByTestId("ssh-agent-key-trigger"));
    await u.click(await screen.findByRole("option", { name: new RegExp(FP_OTHER) }));
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON).agent_key_fingerprint).toBe(FP_OTHER);
  });

  it("编辑:已存指纹当前可用 → 无缺失行,保存可用,config 保持已存指纹", async () => {
    agentSources = [source(1, "System Agent")];
    agentIdentities = [identity(FP_SAVED, "saved key")];
    const onValidity = vi.fn();
    render(
      <SSHConfigSection ref={createRef()} editAsset={agentAsset(FP_SAVED)} ctx={ctx} onValidityChange={onValidity} />
    );

    await waitFor(() => expect(screen.queryByTestId("ssh-agent-key-unavailable")).not.toBeInTheDocument());
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );
  });

  it("切换认证类型清除互斥字段:agent→password 不写 agent 字段", async () => {
    agentSources = [source(1, "System Agent")];
    agentIdentities = [identity(FP_AVAIL)];
    const u = user();
    const ref = createRef<AssetFormHandle>();
    render(<SSHConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);

    await u.click(screen.getByTestId("ssh-auth-type-option-agent"));
    await u.click(await screen.findByTestId("ssh-agent-source-trigger"));
    await u.click(await screen.findByRole("option", { name: "System Agent" }));
    await u.click(await screen.findByTestId("ssh-agent-key-trigger"));
    await u.click(await screen.findByRole("option", { name: new RegExp(FP_AVAIL) }));

    await u.click(screen.getByTestId("ssh-auth-type-option-password"));
    const built = await ref.current!.buildConfig(ctx);
    const parsed = JSON.parse(built.configJSON);
    expect(parsed.auth_type).toBe("password");
    expect(parsed.agent_source_id).toBeUndefined();
    expect(parsed.agent_key_fingerprint).toBeUndefined();
  });
});
