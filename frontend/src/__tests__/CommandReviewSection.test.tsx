import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { CommandReviewSection } from "../components/settings/CommandReviewSection";
import { GetCommandReviewSettings, SaveCommandReviewSettings, TestCommandReview } from "../../wailsjs/go/system/System";

const toastError = vi.hoisted(() => vi.fn());
const notifySuccess = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { error: toastError, warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifySuccess, notifyCopied: vi.fn() }));

const settings = (over: Record<string, unknown> = {}) =>
  ({
    apiKeySet: false,
    baseUrl: "https://api.typesafe.ai",
    model: "jev-1.13.0",
    timeoutMs: 5000,
    threshold: 0.2,
    lastFailReason: "",
    lastFailAt: 0,
    ...over,
  }) as never;

async function renderSection() {
  await act(async () => {
    render(<CommandReviewSection />);
  });
}

const click = async (name: string) => {
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name }));
  });
};

describe("CommandReviewSection", () => {
  beforeEach(() => {
    toastError.mockClear();
    notifySuccess.mockClear();
    vi.mocked(SaveCommandReviewSettings).mockReset().mockResolvedValue(undefined);
    vi.mocked(TestCommandReview).mockReset().mockResolvedValue("jev-1.13.0");
    vi.mocked(GetCommandReviewSettings).mockReset().mockResolvedValue(settings());
  });

  it("加载已保存的设置", async () => {
    await renderSection();
    expect(screen.getByLabelText("commandReview.settings.baseUrl")).toHaveValue("https://api.typesafe.ai");
    expect(screen.getByLabelText("commandReview.settings.model")).toHaveValue("jev-1.13.0");
    expect(screen.getByLabelText("commandReview.settings.timeout")).toHaveValue(5000);
    expect(screen.getByLabelText("commandReview.settings.threshold")).toHaveValue(0.2);
    expect(screen.getByText("commandReview.settings.apiKeyNotSet")).toBeInTheDocument();
  });

  it("保存时带上新填的 API key，保存后清空输入框", async () => {
    await renderSection();
    fireEvent.change(screen.getByLabelText("commandReview.settings.apiKey"), { target: { value: " ts-key " } });
    vi.mocked(GetCommandReviewSettings).mockResolvedValue(settings({ apiKeySet: true }));
    await click("commandReview.settings.save");

    expect(SaveCommandReviewSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        apiKey: "ts-key",
        clearApiKey: false,
        baseUrl: "https://api.typesafe.ai",
        model: "jev-1.13.0",
        timeoutMs: 5000,
        threshold: 0.2,
      })
    );
    expect(screen.getByLabelText("commandReview.settings.apiKey")).toHaveValue("");
    expect(screen.getByText("commandReview.settings.apiKeySet")).toBeInTheDocument();
  });

  // 先试再存：填了不可用的地址或模型，不会影响正在生效的设置。
  it("测试模型用表单里填的值，不保存，并显示服务端实际作答的模型", async () => {
    await renderSection();
    fireEvent.change(screen.getByLabelText("commandReview.settings.apiKey"), { target: { value: "ts-key" } });
    fireEvent.change(screen.getByLabelText("commandReview.settings.baseUrl"), {
      target: { value: "http://10.0.0.5:8080" },
    });
    fireEvent.change(screen.getByLabelText("commandReview.settings.model"), { target: { value: "my-jev" } });
    await click("commandReview.settings.test");

    expect(TestCommandReview).toHaveBeenCalledWith(
      expect.objectContaining({ apiKey: "ts-key", baseUrl: "http://10.0.0.5:8080", model: "my-jev" })
    );
    expect(SaveCommandReviewSettings).not.toHaveBeenCalled();
    expect(notifySuccess).toHaveBeenCalledWith("commandReview.settings.testSuccess");
  });

  it("数值不合法时不保存", async () => {
    await renderSection();
    fireEvent.change(screen.getByLabelText("commandReview.settings.threshold"), { target: { value: "1.5" } });
    await click("commandReview.settings.save");

    expect(SaveCommandReviewSettings).not.toHaveBeenCalled();
    expect(toastError).toHaveBeenCalledWith("commandReview.settings.thresholdRange");
  });

  it("显示最近一次审核失败", async () => {
    vi.mocked(GetCommandReviewSettings).mockResolvedValue(
      settings({ apiKeySet: true, lastFailReason: "invalid_api_key", lastFailAt: Date.now() })
    );
    await renderSection();
    expect(screen.getByTestId("command-review-last-fail")).toHaveTextContent("commandReview.settings.lastFail");
  });

  it("清除 API key", async () => {
    vi.mocked(GetCommandReviewSettings).mockResolvedValue(settings({ apiKeySet: true }));
    await renderSection();
    await click("commandReview.settings.clearApiKey");
    expect(SaveCommandReviewSettings).toHaveBeenCalledWith(expect.objectContaining({ clearApiKey: true, apiKey: "" }));
  });
});
