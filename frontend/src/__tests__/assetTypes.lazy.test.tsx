// 内置类型的 ConfigSection / DetailInfoCard 按需加载：加载期间只在它们自己的槽位里显示占位，
// 表单其余部分与详情页通用信息照常可用；加载完成后渲染真正的区块。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { AssetDetail } from "@/components/asset/AssetDetail";
import { AssetForm } from "@/components/asset/AssetForm";
import { asset_entity } from "../../wailsjs/go/models";
import { GetCommandReviewState } from "../../wailsjs/go/extension/Extension";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), info: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));

const SECTION_LOADING = "asset-type-section-loading";

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(GetCommandReviewState).mockResolvedValue({
    reviewers: [],
    selected: "",
    active: "",
    problem: "no_reviewer",
  } as never);
});

describe("lazy asset-type sections", () => {
  it("shows a local placeholder in the form while the ConfigSection loads, then the section", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);

    expect(screen.getByTestId("asset-form-name-input")).toBeInTheDocument();
    expect(screen.getByTestId(SECTION_LOADING)).toBeInTheDocument();

    expect(await screen.findByTestId("config-tab-advanced")).toBeInTheDocument();
    expect(screen.queryByTestId(SECTION_LOADING)).not.toBeInTheDocument();
  });

  it("shows a local placeholder in the detail page while the DetailInfoCard loads, then the card", async () => {
    const asset = new asset_entity.Asset({
      ID: 3,
      Name: "dev-shell",
      Type: "local",
      Config: JSON.stringify({ shell: "/bin/zsh-lazy" }),
    });
    render(
      <TooltipProvider>
        <AssetDetail asset={asset} isConnecting={false} onEdit={vi.fn()} onDelete={vi.fn()} onConnect={vi.fn()} />
      </TooltipProvider>
    );

    expect(screen.getByText("dev-shell")).toBeInTheDocument();
    expect(screen.getByTestId(SECTION_LOADING)).toBeInTheDocument();

    expect(await screen.findByText("/bin/zsh-lazy")).toBeInTheDocument();
    expect(screen.queryByTestId(SECTION_LOADING)).not.toBeInTheDocument();
  });

  it("does not carry the previous type's validity into a section that is still loading", async () => {
    const local = new asset_entity.Asset({ ID: 1, Name: "shell", Type: "local", Config: "{}" });
    const kafka = new asset_entity.Asset({ ID: 2, Name: "events", Type: "kafka", Config: "{}" });
    const { rerender } = render(<AssetForm open editAsset={local} onOpenChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("asset-form-submit")).toBeEnabled());

    rerender(<AssetForm open editAsset={kafka} onOpenChange={vi.fn()} />);

    expect(screen.getByTestId(SECTION_LOADING)).toBeInTheDocument();
    expect(screen.getByTestId("asset-form-submit")).toBeDisabled();
  });
});
