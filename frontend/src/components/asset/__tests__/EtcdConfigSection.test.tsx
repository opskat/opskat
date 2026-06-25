import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("EtcdConfigSection ref 契约", () => {
  it("编辑态(inline 既有密文):buildConfig 沿用密文 + ssh_asset_id;buildTestConfig 同形,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "etcd",
      Config:
        '{"endpoints":["a:2379","b:2379"],"username":"u","password":"OLD","tls":true,' +
        '"tls_insecure":true,"dial_timeout_seconds":5,"command_timeout_seconds":10,"ssh_asset_id":9}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<EtcdConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"endpoints":["a:2379","b:2379"],"username":"u","password":"OLD","tls":true,' +
        '"tls_insecure":true,"dial_timeout_seconds":5,"command_timeout_seconds":10,"ssh_asset_id":9}',
      sshTunnelId: 9,
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "etcd", configJSON: built.configJSON, password: "" });
  });

  it("创建态(无端点):上报 canSave/canTest=false + etcd.error.endpointsRequired", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<EtcdConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "etcd.error.endpointsRequired",
    });
  });

  it("编辑态(有端点):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({ Type: "etcd", Config: '{"endpoints":["a:2379"]}' });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<EtcdConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
