import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CustomTypeSection } from "@/components/settings/CustomTypeSection";
import { useCustomTypeStore } from "@/stores/customTypeStore";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

describe("CustomTypeSection", () => {
  beforeEach(async () => {
    toastError.mockClear();
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.ListCustomTypes).mockReset();
    vi.mocked(mod.DeleteCustomType).mockReset();
    useCustomTypeStore.setState({ types: [], loading: false, loaded: false });
  });

  it("lists each type's execution mode and asset count", async () => {
    const { ListCustomTypes } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 3 },
      { id: 2, slug: "aws-cli", name: "AWS CLI", icon: "", execMode: "command", assetCount: 0 },
    ]);

    render(<CustomTypeSection />);

    expect(await screen.findByText("Grafana")).toBeInTheDocument();
    expect(screen.getByText("AWS CLI")).toBeInTheDocument();
    expect(screen.getByText(/grafana/)).toBeInTheDocument();
    expect(screen.getAllByText("customType.assetCount")).toHaveLength(2);
  });

  it("refuses to delete an in-use type and reports the blocking assets instead of removing it", async () => {
    const { ListCustomTypes, DeleteCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 1 },
    ]);
    vi.mocked(DeleteCustomType).mockResolvedValue({ deleted: false, assets: ["grafana-prod"] });

    render(<CustomTypeSection />);
    const user = userEvent.setup();
    await screen.findByText("Grafana");

    await user.click(screen.getByRole("button", { name: "action.delete" }));
    const confirmButtons = await screen.findAllByRole("button", { name: "action.delete" });
    await user.click(confirmButtons[confirmButtons.length - 1]);

    // 拒绝原因走 toast.error(customType.deleteInUse),而不是当作删除成功处理
    // (mocked t() 只回显 key,资产名是否正确传参由 customTypeStore.test.ts 覆盖)。
    expect(toastError).toHaveBeenCalledWith(expect.stringContaining("customType.deleteInUse"));
    // 仍在列表里——没有被当作已删除处理
    expect(screen.getByText("Grafana")).toBeInTheDocument();
  });
});
