import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { PermissionModeCard } from "@/components/asset/PermissionModeCard";
import { useAssetStore } from "@/stores/assetStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { useTabStore } from "@/stores/tabStore";
import { GetCommandReviewState } from "../../wailsjs/go/extension/Extension";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));

// 在用的审核器是 jev-command-review，但它的必填设置（比如 API key）没填完。
const notConfigured = {
  reviewers: [
    { name: "jev-command-review", displayName: "Jev", version: "1.0.0", usable: true, signed: true, questions: [] },
  ],
  selected: "",
  active: "jev-command-review",
  problem: "not_configured",
};

async function renderCard(value: string) {
  await act(async () => {
    render(<PermissionModeCard value={value} subject="asset" parentGroupId={0} onChange={vi.fn()} />);
  });
}

describe("PermissionModeCard：审核器未配置", () => {
  beforeEach(() => {
    vi.mocked(GetCommandReviewState).mockResolvedValue(notConfigured as never);
    useAssetStore.setState({ groups: [] });
    useTabStore.setState({ tabs: [], activeTabId: null });
    useSettingsUiStore.setState({ activeTab: "ai", extensionDetail: null });
  });

  it.each(["assisted", "autopilot"])("%s 模式下提示审核器未配置，并能打开它的设置", async (mode) => {
    await renderCard(mode);
    expect(screen.queryByTestId("permission-mode-no-reviewer")).toBeNull();
    const notice = screen.getByTestId("permission-mode-reviewer-not-configured");
    expect(notice).toHaveTextContent("commandReview.mode.notConfigured");

    fireEvent.click(within(notice).getByRole("button", { name: "commandReview.settings.openReviewerSettings" }));
    expect(useSettingsUiStore.getState().activeTab).toBe("extensions");
    expect(useSettingsUiStore.getState().extensionDetail).toBe("jev-command-review");
    expect(useTabStore.getState().activeTabId).toBe("settings");
  });

  it("默认模式下不提示", async () => {
    await renderCard("default");
    expect(screen.queryByTestId("permission-mode-reviewer-not-configured")).toBeNull();
  });
});
