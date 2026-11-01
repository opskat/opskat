import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { CommandReviewSection } from "../components/settings/CommandReviewSection";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { GetCommandReviewSettings, SaveCommandReviewTimeout } from "../../wailsjs/go/system/System";
import { GetCommandReviewState, SetCommandReviewer } from "../../wailsjs/go/extension/Extension";

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
  });

  it("显示在用的审核扩展；它的设置（含保存并测试）在扩展详情里，这里给入口", async () => {
    await renderSection();
    expect(screen.getByTestId("command-reviewer-active")).toHaveTextContent("JEV");
    expect(screen.queryByTestId("command-review-problem")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "commandReview.settings.configureReviewer" }));
    expect(useSettingsUiStore.getState().activeTab).toBe("extensions");
    expect(useSettingsUiStore.getState().extensionDetail).toBe("jev");
  });

  it("没有审核扩展时提示去安装，并说明辅助审批和 Autopilot 会转到默认", async () => {
    vi.mocked(GetCommandReviewState).mockResolvedValue(state({ reviewers: [], active: "", problem: "no_reviewer" }));
    await renderSection();
    expect(screen.getByTestId("command-review-problem")).toHaveTextContent("commandReview.settings.noReviewer");
  });

  it("启用了多个审核扩展时让用户选一个；用不了的不能选并写明原因", async () => {
    vi.mocked(GetCommandReviewState).mockResolvedValue(
      state({
        reviewers: [
          reviewer("a"),
          reviewer("b", { signed: false }),
          reviewer("bad", { usable: false, reason: "digest_mismatch" }),
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

  it("打开着时随审核状态事件实时出现失败、恢复后消失，不重新挂载", async () => {
    const handlers = new Map<string, (data: unknown) => void>();
    vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
      handlers.set(event, handler);
      return vi.fn();
    }) as never);
    const { unmount } = await act(async () => render(<CommandReviewSection />));
    expect(screen.queryByTestId("command-review-last-fail")).toBeNull();
    const fetches = vi.mocked(GetCommandReviewSettings).mock.calls.length;

    act(() => handlers.get("command-review:status")?.({ lastFailReason: "timeout", lastFailAt: 1700000000000 }));
    expect(screen.getByTestId("command-review-last-fail")).toHaveTextContent("commandReview.settings.lastFail");

    act(() => handlers.get("command-review:status")?.({ lastFailReason: "", lastFailAt: 0 }));
    expect(screen.queryByTestId("command-review-last-fail")).toBeNull();
    expect(GetCommandReviewSettings).toHaveBeenCalledTimes(fetches);

    unmount();
    expect(EventsOff).toHaveBeenCalledWith("command-review:status");
  });

  it("状态事件早于设置读取的回包到达时，不被回包里读取时的旧状态盖掉", async () => {
    const handlers = new Map<string, (data: unknown) => void>();
    vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
      handlers.set(event, handler);
      return vi.fn();
    }) as never);
    let answer: (v: unknown) => void = () => {};
    vi.mocked(GetCommandReviewSettings).mockReturnValue(new Promise((r) => (answer = r)) as never);
    await renderSection();

    // 后端读完状态（还没失败）之后才出现失败：事件先到，回包后到。
    act(() => handlers.get("command-review:status")?.({ lastFailReason: "timeout", lastFailAt: 1700000000000 }));
    await act(async () => answer({ timeoutMs: 5000, lastFailReason: "", lastFailAt: 0 }));
    expect(screen.getByTestId("command-review-last-fail")).toHaveTextContent("commandReview.settings.lastFail");

    // 保存超时后重新读取同样不能盖掉已推送的状态。
    vi.mocked(GetCommandReviewSettings).mockResolvedValue({
      timeoutMs: 8000,
      lastFailReason: "",
      lastFailAt: 0,
    } as never);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "commandReview.settings.save" }));
    });
    expect(screen.getByTestId("command-review-last-fail")).toHaveTextContent("commandReview.settings.lastFail");
  });
});
