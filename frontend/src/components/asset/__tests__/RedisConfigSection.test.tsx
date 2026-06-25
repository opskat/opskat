import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("RedisConfigSection ref 契约", () => {
  it("编辑态(inline 既有密文):save 沿用密文且省略 ssh_asset_id(隧道走顶层);test config 含 ssh_asset_id,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config:
        '{"host":"127.0.0.1","port":6379,"username":"u","password":"OLD","database":2,' +
        '"tls":true,"tls_insecure":true,"command_timeout_seconds":30,"scan_page_size":200,"ssh_asset_id":9}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    // save:沿用既有密文,但不写 ssh_asset_id —— 隧道走 asset 顶层列(锁旧 save)。
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"host":"127.0.0.1","port":6379,"username":"u","password":"OLD","database":2,' +
        '"tls":true,"tls_insecure":true,"command_timeout_seconds":30,"scan_page_size":200}',
      sshTunnelId: 9,
    });
    // test:无 asset 行 → config 末尾带 ssh_asset_id(锁旧 handleTestRedisConnection)。
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({
      assetType: "redis",
      configJSON:
        '{"host":"127.0.0.1","port":6379,"username":"u","password":"OLD","database":2,' +
        '"tls":true,"tls_insecure":true,"command_timeout_seconds":30,"scan_page_size":200,"ssh_asset_id":9}',
      password: "",
    });
  });

  it("创建态(无 host):上报 canSave/canTest=false + asset.formMissingHost", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<RedisConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingHost",
    });
  });

  it("编辑态(有 host):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({ Type: "redis", Config: '{"host":"127.0.0.1","port":6379}' });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
