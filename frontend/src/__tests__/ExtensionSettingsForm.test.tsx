import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { ExtensionSettingsForm } from "../components/settings/ExtensionSettingsForm";
import { GetExtensionSettings, SaveExtensionSettings, TestCommandReviewer } from "../../wailsjs/go/extension/Extension";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));

const view = {
  schema: {
    type: "object",
    properties: {
      endpoint: { type: "string", format: "endpoint", title: "Endpoint" },
      token: { type: "string", format: "password", title: "API key" },
    },
  },
  values: { endpoint: "https://review.example.com" },
  secretsSet: ["token"],
};

describe("ExtensionSettingsForm", () => {
  beforeEach(() => {
    vi.mocked(GetExtensionSettings)
      .mockReset()
      .mockResolvedValue(view as never);
    vi.mocked(SaveExtensionSettings).mockReset().mockResolvedValue(undefined);
  });

  it("读回非密钥字段，已设置的密钥只显示为已设置，留空保存不提交新值", async () => {
    await act(async () => {
      render(<ExtensionSettingsForm extensionName="command-review" />);
    });
    expect(await screen.findByDisplayValue("https://review.example.com")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("asset.passwordUnchanged")).toHaveValue("");

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "extension.settings.save" }));
    });
    expect(SaveExtensionSettings).toHaveBeenCalledWith("command-review", { endpoint: "https://review.example.com" });
  });

  it("必填项没填时在字段下提示、不提交；已存过的密钥留空不算没填", async () => {
    vi.mocked(GetExtensionSettings).mockResolvedValue({
      ...view,
      schema: { ...view.schema, required: ["endpoint", "token"] },
      values: { endpoint: "" },
      secretsSet: [],
    } as never);
    await act(async () => {
      render(<ExtensionSettingsForm extensionName="command-review" />);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "extension.settings.save" }));
    });
    expect(SaveExtensionSettings).not.toHaveBeenCalled();
    expect(screen.getAllByText("extension.settings.required")).toHaveLength(2);
  });

  it("已存过的必填密钥留空可以保存", async () => {
    vi.mocked(GetExtensionSettings).mockResolvedValue({
      ...view,
      schema: { ...view.schema, required: ["endpoint", "token"] },
    } as never);
    await act(async () => {
      render(<ExtensionSettingsForm extensionName="command-review" />);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "extension.settings.save" }));
    });
    expect(SaveExtensionSettings).toHaveBeenCalledWith("command-review", { endpoint: "https://review.example.com" });
  });

  it("审核扩展可以保存并测试：先保存，再用刚保存的设置测这个扩展，显示结果", async () => {
    vi.mocked(TestCommandReviewer).mockResolvedValue({ mode: "", outcome: "fail", reason: "invalid_api_key" } as never);
    await act(async () => {
      render(<ExtensionSettingsForm extensionName="command-review" reviewer />);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "extension.settings.saveAndTest" }));
    });
    expect(SaveExtensionSettings).toHaveBeenCalled();
    expect(TestCommandReviewer).toHaveBeenCalledWith("command-review");
    expect(screen.getByTestId("extension-settings-test-result")).toHaveTextContent("commandReview.summary.fail");
  });

  it("不是审核扩展时没有测试按钮", async () => {
    await act(async () => {
      render(<ExtensionSettingsForm extensionName="notebook" />);
    });
    expect(screen.queryByRole("button", { name: "extension.settings.saveAndTest" })).toBeNull();
  });
});
