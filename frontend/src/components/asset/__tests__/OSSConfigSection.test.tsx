import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { OSSConfigSection } from "@/components/asset/OSSConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("OSSConfigSection ref 契约", () => {
  it("编辑态(inline 既有密文):buildConfig 沿用密文,sshTunnelId 恒 0;buildTestConfig 同形,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "oss",
      Config:
        '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
        '"access_key_id":"AKIA","secret_access_key":"OLD","use_path_style":true,"use_ssl":false,"connect_timeout":30}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<OSSConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
        '"access_key_id":"AKIA","secret_access_key":"OLD","use_path_style":true,"use_ssl":false,"connect_timeout":30}',
      sshTunnelId: 0,
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "oss", configJSON: built.configJSON, password: "" });
  });

  it("创建态(无 endpoint/AK):上报 canSave/canTest=false + oss.error.required", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<OSSConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "oss.error.required",
    });
  });

  it("编辑态(有 endpoint+AK):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({
      Type: "oss",
      Config: '{"endpoint":"http://localhost:9000","access_key_id":"AKIA"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<OSSConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
