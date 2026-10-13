import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { AssetDetail } from "@/components/asset/AssetDetail";
import { asset_entity } from "../../wailsjs/go/models";
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
});
