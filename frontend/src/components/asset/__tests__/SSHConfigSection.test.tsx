import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

vi.mock("../../../../wailsjs/go/ssh/SSH", () => ({
  ListLocalSSHKeys: () => Promise.resolve([]),
  SelectSSHKeyFile: () => Promise.resolve(null),
}));

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
