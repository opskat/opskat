import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { AssetDetail } from "@/components/asset/AssetDetail";
import { AssetForm } from "@/components/asset/AssetForm";
import { asset_entity } from "../../wailsjs/go/models";
import { TestAssetConnection, UpdateAsset } from "../../wailsjs/go/system/System";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

// 资产配置里的驱动不在驱动注册表里(例如提供它的扩展被卸载):查询面板报「驱动不可用」,
// 资产本身仍然要能删除。

const ghostAsset = new asset_entity.Asset({
  ID: 7,
  Name: "orders-db",
  Type: "database",
  Config: JSON.stringify({ driver: "ghostdb", host: "10.0.0.5", port: 1521, username: "app" }),
});

beforeEach(() => {
  vi.clearAllMocks();
  provideDrivers(BUILTIN_DRIVERS);
});

describe("驱动未注册的数据库资产", () => {
  it("详情页能展示并删除", async () => {
    const user = userEvent.setup();
    const onDelete = vi.fn();
    render(
      <TooltipProvider>
        <AssetDetail asset={ghostAsset} isConnecting={false} onEdit={vi.fn()} onDelete={onDelete} onConnect={vi.fn()} />
      </TooltipProvider>
    );

    // 详情卡里驱动一栏是资产配置里的原值。
    expect(await screen.findByText("ghostdb")).toBeInTheDocument();

    await user.click(screen.getByLabelText("action.delete"));
    const buttons = await screen.findAllByRole("button", { name: "action.delete" });
    await user.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(onDelete).toHaveBeenCalledTimes(1));
  });

  it("可以编辑:改名后保存,驱动与连接配置原样保留,仍提示驱动未知", async () => {
    const user = userEvent.setup();
    vi.mocked(UpdateAsset).mockResolvedValue(undefined as never);
    render(<AssetForm open editAsset={ghostAsset} onOpenChange={vi.fn()} />);

    expect(await screen.findByText("asset.driverUnknown")).toBeInTheDocument();
    const name = await screen.findByDisplayValue("orders-db");
    await user.clear(name);
    await user.type(name, "orders-db-renamed");
    await user.click(screen.getByTestId("asset-form-submit"));

    await waitFor(() => expect(UpdateAsset).toHaveBeenCalledTimes(1));
    const saved = vi.mocked(UpdateAsset).mock.calls[0][0] as asset_entity.Asset;
    expect(saved.Name).toBe("orders-db-renamed");
    expect(saved.Config).toBe(ghostAsset.Config);
  });

  it("测试连接把原配置交给后端,由后端回答驱动不可用,界面不报成功", async () => {
    const user = userEvent.setup();
    vi.mocked(TestAssetConnection).mockRejectedValue(new Error('database driver "ghostdb" is unavailable'));
    render(<AssetForm open editAsset={ghostAsset} onOpenChange={vi.fn()} />);

    await user.click(await screen.findByTestId("asset-test-connection"));

    await waitFor(() => expect(TestAssetConnection).toHaveBeenCalledTimes(1));
    expect(vi.mocked(TestAssetConnection).mock.calls[0][2]).toBe(ghostAsset.Config);
  });
});
