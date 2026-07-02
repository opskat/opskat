import { beforeEach, describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetForm } from "@/components/asset/AssetForm";
import { GetSSHConnectionSettings } from "../../wailsjs/go/system/System";

// 资产 store 与扩展 store 在测试里走真实实现 + 全局 Wails mock(setup.ts)即可渲染创建态。
describe("AssetForm description bar", () => {
  beforeEach(() => {
    vi.mocked(GetSSHConnectionSettings).mockResolvedValue({ keepAliveIntervalSeconds: 90 });
  });

  it("renders the collapsible description bar in create mode", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    const add = await screen.findByTestId("description-add");
    expect(add).toBeInTheDocument();
    await userEvent.click(add);
    expect(screen.getByTestId("description-textarea")).toBeInTheDocument();
  });

  it("prefills the create-mode SSH keep-alive field with the global value", async () => {
    const u = userEvent.setup();
    render(<AssetForm open onOpenChange={vi.fn()} />);

    await u.click(await screen.findByTestId("config-tab-advanced"));

    const input = await screen.findByTestId("ssh-keepalive-input");
    await waitFor(() => expect(input).toHaveValue(90));
  });
});
