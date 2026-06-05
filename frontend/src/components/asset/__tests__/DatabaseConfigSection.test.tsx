import { describe, it, expect, vi, beforeAll } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
  SelectSQLiteFile: () => Promise.resolve(""),
}));

// Radix Select 在 happy-dom 无 layout/pointer-capture,补齐 user-event 驱动所需的最小桩。
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("DatabaseConfigSection ref 契约", () => {
  it("创建态默认 mysql(network):无 host → canSave/canTest=false + asset.formMissingHost", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingHost",
    });
  });

  it("编辑态(network 有 host):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"mysql","host":"127.0.0.1","port":3306}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("编辑态(sqlite 无 path):上报 canSave/canTest=false + asset.formMissingPath", () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingPath",
    });
  });

  it("编辑态(sqlite 有 path):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite","path":"/tmp/x.db"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("编辑态(postgresql + ssl_mode,inline 既有密文):buildConfig 沿用密文 + ssh_asset_id;buildTestConfig 同形,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config:
        '{"driver":"postgresql","host":"pg.example.com","port":5432,"username":"postgres","password":"OLD",' +
        '"ssh_asset_id":5,"ssl_mode":"require","database":"mydb"}',
      sshTunnelId: 5,
    });
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"driver":"postgresql","host":"pg.example.com","port":5432,"username":"postgres","password":"OLD",' +
        '"ssh_asset_id":5,"ssl_mode":"require","database":"mydb"}',
      sshTunnelId: 5,
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "database", configJSON: built.configJSON, password: "" });
  });

  it("编辑态(sqlite):buildConfig 仅 path/database/read_only,无凭据/host/ssh", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite","path":"/tmp/x.db","database":"main","read_only":true}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON: '{"driver":"sqlite","path":"/tmp/x.db","database":"main","read_only":true}',
      sshTunnelId: 0,
    });
  });

  it("编辑态(managed 凭据):buildConfig 写 credential_id,不写 password", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"mysql","host":"127.0.0.1","port":3306,"username":"root","credential_id":7}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain('"credential_id":7');
    expect(built.configJSON).not.toContain('"password"');
  });

  it("driver 切到 sqlite:触发 onIconChange('sqlite') 且切到 path 校验(formMissingPath)", async () => {
    const user = userEvent.setup();
    const onIcon = vi.fn();
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} onIconChange={onIcon} />);
    // 打开 driver Select(显示当前值 asset.driverMySQL;页面另有密码来源 combobox),选 SQLite。
    const driverSelect = screen.getAllByRole("combobox").find((el) => el.textContent?.includes("asset.driverMySQL"));
    await user.click(driverSelect!);
    await user.click(await screen.findByText("asset.driverSQLite"));
    expect(onIcon).toHaveBeenCalledWith("sqlite");
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingPath",
    });
  });
});
