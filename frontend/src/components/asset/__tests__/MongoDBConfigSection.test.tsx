import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { MongoDBConfigSection } from "@/components/asset/MongoDBConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("MongoDBConfigSection ref 契约", () => {
  it("创建态(manual,无 host):上报 canSave/canTest=false + asset.formMissingHost", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingHost",
    });
  });

  it("编辑态(manual,有 host):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({
      Type: "mongodb",
      Config: '{"host":"127.0.0.1","port":27017}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("编辑态(uri 模式,有 connection_uri):上报 canSave/canTest=true", () => {
    const editAsset = new asset_entity.Asset({
      Type: "mongodb",
      Config: '{"connection_uri":"mongodb://user:pass@host:27017/db"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("编辑态(uri 模式,空 uri):上报 canSave/canTest=false + asset.formMissingMongoUri", () => {
    // connection_uri 存在但空字符串 → 解析为 manual 模式(parseMongoDBConfig 取 connection_uri?)
    // 若 Config 无 connection_uri 键 → manual; 若为空字符串 → manual
    const editAsset = new asset_entity.Asset({
      Type: "mongodb",
      Config: '{"host":""}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    // host 为空且 manual 模式 → formMissingHost
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingHost",
    });
  });

  it("编辑态(manual,inline 既有密文):save 沿用密文且省略 ssh_asset_id(隧道走顶层);test config 含 ssh_asset_id,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "mongodb",
      Config:
        '{"host":"127.0.0.1","port":27017,"username":"admin","password":"OLD",' +
        '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true,"ssh_asset_id":5}',
      sshTunnelId: 5,
    });
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    // save:沿用既有密文,但不写 ssh_asset_id —— 隧道走 asset 顶层列(锁旧 save)。
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"host":"127.0.0.1","port":27017,"username":"admin","password":"OLD",' +
        '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true}',
      sshTunnelId: 5,
    });
    // test:无 asset 行 → config 末尾带 ssh_asset_id(锁旧 handleTestMongoDBConnection)。
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({
      assetType: "mongodb",
      configJSON:
        '{"host":"127.0.0.1","port":27017,"username":"admin","password":"OLD",' +
        '"replica_set":"rs0","auth_source":"admin","database":"mydb","tls":true,"ssh_asset_id":5}',
      password: "",
    });
  });

  it("编辑态(uri 模式):buildConfig 写 connection_uri,不含 host/port", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "mongodb",
      Config: '{"connection_uri":"mongodb://user:pass@host:27017/db","username":"admin"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain('"connection_uri":"mongodb://user:pass@host:27017/db"');
    expect(built.configJSON).not.toContain('"host":');
    expect(built.configJSON).not.toContain('"port":');
  });

  it("编辑态(managed 凭据):buildConfig 写 credential_id,不写 password", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "mongodb",
      Config: '{"host":"127.0.0.1","port":27017,"credential_id":7}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<MongoDBConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain('"credential_id":7');
    expect(built.configJSON).not.toContain('"password"');
  });
});
