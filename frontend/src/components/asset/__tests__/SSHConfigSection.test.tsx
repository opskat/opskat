import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent, { PointerEventsCheckLevel } from "@testing-library/user-event";
import { createRef } from "react";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity, credential_entity } from "../../../../wailsjs/go/models";

// 托管凭据按类型注入:autofill 用例往这里塞 password / ssh_key 凭据,
// ref-契约用例保持默认空数组(与原 mock 行为一致)。
let pwCreds: credential_entity.Credential[] = [];
let keyCreds: credential_entity.Credential[] = [];

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: (type: string) =>
    Promise.resolve(type === "ssh_key" ? keyCreds : type === "password" ? pwCreds : []),
  GetAssetPassword: () => Promise.resolve(""),
}));

vi.mock("../../../../wailsjs/go/ssh/SSH", () => ({
  ListLocalSSHKeys: () => Promise.resolve([]),
  SelectSSHKeyFile: () => Promise.resolve(null),
}));

beforeEach(() => {
  pwCreds = [];
  keyCreds = [];
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
        '"proxy":{"type":"socks5","host":"px","port":1080,"username":"pu","password":"PXENC"}}'
    );
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).toBe(
      '{"host":"h","port":22,"username":"u","auth_type":"password",' +
        '"proxy":{"type":"socks5","host":"px","port":1080,"username":"pu"}}'
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
    expect(built.configJSON).toBe('{"host":"h","port":22,"username":"u","auth_type":"password"}');
    expect(built.configJSON).not.toContain("jump_host_id");
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).toBe('{"host":"h","port":22,"username":"u","auth_type":"password","jump_host_id":42}');
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
