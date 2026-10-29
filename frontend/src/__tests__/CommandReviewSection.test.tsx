import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { CommandReviewSection } from "../components/settings/CommandReviewSection";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { GetCommandReviewSettings, SaveCommandReviewTimeout } from "../../wailsjs/go/system/System";
import { GetCommandReviewState, GetExtensionSettings, SetCommandReviewer } from "../../wailsjs/go/extension/Extension";

const toastError = vi.hoisted(() => vi.fn());
const notifySuccess = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { error: toastError, warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifySuccess, notifyCopied: vi.fn() }));

const reviewer = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  displayName: name.toUpperCase(),
  version: "1.0.0",
  usable: true,
  signed: true,
  hasSettings: true,
  ...over,
});

const state = (over: Record<string, unknown> = {}) =>
  ({ reviewers: [reviewer("jev")], selected: "", active: "jev", problem: "", ...over }) as never;

async function renderSection() {
  await act(async () => {
    render(<CommandReviewSection />);
  });
}

describe("CommandReviewSection", () => {
  beforeEach(() => {
    toastError.mockClear();
    notifySuccess.mockClear();
    vi.mocked(GetCommandReviewState).mockReset().mockResolvedValue(state());
    vi.mocked(GetCommandReviewSettings)
      .mockReset()
      .mockResolvedValue({ timeoutMs: 5000, lastFailReason: "", lastFailAt: 0 } as never);
    vi.mocked(SaveCommandReviewTimeout).mockReset().mockResolvedValue(undefined);
    vi.mocked(SetCommandReviewer).mockReset().mockResolvedValue(undefined);
    vi.mocked(GetExtensionSettings)
      .mockReset()
      .mockResolvedValue({ schema: { type: "object", properties: {} }, values: {}, secretsSet: [] } as never);
  });

  it("显示在用的审核扩展；它的设置在扩展详情里，这里只给入口", async () => {
    await renderSection();
    expect(screen.getByTestId("command-reviewer-active")).toHaveTextContent("JEV");
    expect(GetExtensionSettings).not.toHaveBeenCalled();
    expect(screen.queryByTestId("command-review-problem")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "commandReview.settings.configureReviewer" }));
    expect(useSettingsUiStore.getState().activeTab).toBe("extensions");
    expect(useSettingsUiStore.getState().extensionDetail).toBe("jev");
  });

  it("没有审核扩展时提示去安装，并说明辅助审批和 Autopilot 会转到默认", async () => {
    vi.mocked(GetCommandReviewState).mockResolvedValue(state({ reviewers: [], active: "", problem: "no_reviewer" }));
    await renderSection();
    expect(screen.getByTestId("command-review-problem")).toHaveTextContent("commandReview.settings.noReviewer");
    expect(GetExtensionSettings).not.toHaveBeenCalled();
  });

  it("启用了多个审核扩展时让用户选一个；用不了的不能选并写明原因", async () => {
    vi.mocked(GetCommandReviewState).mockResolvedValue(
      state({
        reviewers: [
          reviewer("a"),
          reviewer("b", { signed: false }),
          reviewer("bad", { usable: false, reason: "digest" }),
        ],
        active: "",
        problem: "no_reviewer",
      })
    );
    await renderSection();
    expect(screen.getByTestId("command-review-problem")).toHaveTextContent("commandReview.settings.selectReviewer");
    expect(screen.getByTestId("command-reviewer-b")).toHaveTextContent("commandReview.settings.unsigned");
    expect(screen.getByTestId("command-reviewer-bad")).toBeDisabled();
    expect(screen.getByTestId("command-reviewer-unusable")).toHaveTextContent(
      "commandReview.settings.reviewerUnusable"
    );

    vi.mocked(GetCommandReviewState).mockResolvedValue(
      state({ reviewers: [reviewer("a"), reviewer("b")], selected: "a", active: "a" })
    );
    await act(async () => {
      fireEvent.click(screen.getByTestId("command-reviewer-a"));
    });
    expect(SetCommandReviewer).toHaveBeenCalledWith("a");
  });

  it("保存超时，超出范围不保存", async () => {
    await renderSection();
    const input = screen.getByLabelText("commandReview.settings.timeout");
    fireEvent.change(input, { target: { value: "500" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "commandReview.settings.save" }));
    });
    expect(SaveCommandReviewTimeout).not.toHaveBeenCalled();
    expect(toastError).toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "8000" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "commandReview.settings.save" }));
    });
    expect(SaveCommandReviewTimeout).toHaveBeenCalledWith(8000);
  });

  it("显示最近一次审核失败", async () => {
    vi.mocked(GetCommandReviewSettings).mockResolvedValue({
      timeoutMs: 5000,
      lastFailReason: "invalid_api_key",
      lastFailAt: 1,
    } as never);
    await renderSection();
    expect(screen.getByTestId("command-review-last-fail")).toHaveTextContent("commandReview.settings.lastFail");
  });
});
