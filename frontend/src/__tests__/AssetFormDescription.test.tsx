import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetForm } from "@/components/asset/AssetForm";

// 资产 store 与扩展 store 在测试里走真实实现 + 全局 Wails mock(setup.ts)即可渲染创建态。
describe("AssetForm description bar", () => {
  it("renders the collapsible description bar in create mode", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    const add = await screen.findByTestId("description-add");
    expect(add).toBeInTheDocument();
    await userEvent.click(add);
    expect(screen.getByTestId("description-textarea")).toBeInTheDocument();
  });
});
