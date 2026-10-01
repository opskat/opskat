import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { CommandReviewConfigErrorListener } from "../components/settings/CommandReviewConfigErrorListener";
import { useSettingsUiStore } from "../stores/settingsUiStore";
import { useTabStore } from "../stores/tabStore";

const warning = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { warning, error: vi.fn(), info: vi.fn(), success: vi.fn() } }));

function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

describe("CommandReviewConfigErrorListener", () => {
  beforeEach(() => warning.mockClear());

  it("收到配置错误时提醒，并能直接打开设置 › AI", () => {
    const handlers = captureHandlers();
    render(<CommandReviewConfigErrorListener />);

    act(() => handlers.get("command-review:config-error")?.("invalid_api_key"));

    expect(warning).toHaveBeenCalledTimes(1);
    const [message, options] = warning.mock.calls[0];
    expect(message).toBe("commandReview.configError.invalid_api_key");

    useSettingsUiStore.setState({ activeTab: "backup" });
    act(() => options.action.onClick());
    expect(useSettingsUiStore.getState().activeTab).toBe("ai");
    expect(useTabStore.getState().tabs.some((tab) => tab.id === "settings")).toBe(true);
  });

  it("已保存的 API key 读不出来也是配置错误，要提醒", () => {
    const handlers = captureHandlers();
    render(<CommandReviewConfigErrorListener />);

    act(() => handlers.get("command-review:config-error")?.("api_key_unreadable"));

    expect(warning).toHaveBeenCalledTimes(1);
    expect(warning.mock.calls[0][0]).toBe("commandReview.configError.api_key_unreadable");
  });

  it("不认识的原因不提醒", () => {
    const handlers = captureHandlers();
    render(<CommandReviewConfigErrorListener />);

    act(() => handlers.get("command-review:config-error")?.("timeout"));
    expect(warning).not.toHaveBeenCalled();
  });
});
