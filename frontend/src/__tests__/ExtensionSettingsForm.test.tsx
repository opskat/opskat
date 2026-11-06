import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { ExtensionSettingsForm } from "../components/settings/ExtensionSettingsForm";
import { GetExtensionSettings, SaveExtensionSettings, TestCommandReviewer } from "../../wailsjs/go/extension/Extension";

const toastError = vi.hoisted(() => vi.fn());
const notifySuccess = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { error: toastError, warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifySuccess, notifyCopied: vi.fn() }));

const form = (over: Record<string, unknown> = {}) =>
  ({
    configSchema: {
      type: "object",
      properties: {
        baseURL: { type: "string", format: "endpoint", title: "Service URL" },
        apiKey: { type: "string", format: "password", title: "API key" },
      },
      required: ["baseURL", "apiKey"],
      propertyOrder: ["baseURL", "apiKey"],
    },
    values: { baseURL: "https://jev.example" },
    secrets: { apiKey: true },
    ...over,
  }) as never;

async function renderForm() {
  await act(async () => {
    render(<ExtensionSettingsForm extName="jev" />);
  });
}

describe("ExtensionSettingsForm", () => {
  beforeEach(() => {
    toastError.mockClear();
    notifySuccess.mockClear();
    vi.mocked(GetExtensionSettings).mockReset().mockResolvedValue(form());
    vi.mocked(SaveExtensionSettings).mockReset().mockResolvedValue([]);
  });

  it("renders the declared fields with the stored values, a set secret only as set", async () => {
    await renderForm();
    expect(screen.getByLabelText(/Service URL/)).toHaveValue("https://jev.example");
    const key = screen.getByLabelText(/API key/);
    expect(key).toHaveValue("");
    expect(key).toHaveAttribute("placeholder", "asset.passwordUnchanged");
  });

  it("an unset secret is not shown as set", async () => {
    vi.mocked(GetExtensionSettings).mockResolvedValue(form({ secrets: { apiKey: false } }));
    await renderForm();
    expect(screen.getByLabelText(/API key/)).not.toHaveAttribute("placeholder", "asset.passwordUnchanged");
  });

  it("starts a field nothing was saved for from its declared default, as the asset config form does", async () => {
    const base = form() as unknown as { configSchema: { properties: Record<string, Record<string, unknown>> } };
    base.configSchema.properties.baseURL.default = "https://api.example";
    vi.mocked(GetExtensionSettings).mockResolvedValue({ ...base, values: {}, secrets: { apiKey: false } } as never);
    await renderForm();
    expect(screen.getByLabelText(/Service URL/)).toHaveValue("https://api.example");
  });

  it("renders nothing for an extension declaring no settings", async () => {
    vi.mocked(GetExtensionSettings).mockResolvedValue(null as never);
    await renderForm();
    expect(screen.queryByTestId("extension-settings")).toBeNull();
  });

  it("saves the edited values, leaving a blank secret for the host to keep", async () => {
    await renderForm();
    fireEvent.change(screen.getByLabelText(/Service URL/), { target: { value: "https://other.example" } });
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    expect(SaveExtensionSettings).toHaveBeenCalledWith("jev", JSON.stringify({ baseURL: "https://other.example" }));
    expect(notifySuccess).toHaveBeenCalledWith("extension.settings.saved");
    expect(GetExtensionSettings).toHaveBeenCalledTimes(2); // read back after the save
  });

  it("shows the host's field errors on their fields and reports none saved", async () => {
    vi.mocked(SaveExtensionSettings).mockResolvedValue([
      { field: "baseURL", code: "required" },
      { field: "extra", code: "unknown" },
    ] as never);
    await renderForm();
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    expect(screen.getByLabelText(/Service URL/)).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("extension.settings.error.required")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("extension.settings.invalid");
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("lists the errors of fields not shown in the form at its top, by their titles", async () => {
    const base = form() as unknown as { configSchema: { properties: Record<string, unknown> } };
    base.configSchema.properties.token = { type: "string", format: "password", title: "Token" };
    vi.mocked(GetExtensionSettings).mockResolvedValue({
      ...base,
      auth: {
        selector: "mode",
        groups: [
          { when: "key", fields: ["apiKey"] },
          { when: "token", fields: ["token"] },
        ],
      },
      values: { baseURL: "https://jev.example", mode: "key" },
      secrets: { apiKey: true, token: false },
    } as never);
    vi.mocked(SaveExtensionSettings).mockResolvedValue([
      { field: "token", code: "required" },
      { field: "extra", code: "unknown" },
    ] as never);
    await renderForm();
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    const hidden = screen.getByTestId("extension-settings-hidden-errors");
    expect(hidden).toHaveTextContent("Token");
    expect(hidden).toHaveTextContent("extension.settings.error.required");
    expect(hidden).toHaveTextContent("extra");
    expect(hidden).toHaveTextContent("extension.settings.error.unknown");
  });

  it("clears the listed errors once the save succeeds", async () => {
    vi.mocked(SaveExtensionSettings).mockResolvedValueOnce([{ field: "extra", code: "unknown" }] as never);
    await renderForm();
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    expect(screen.getByTestId("extension-settings-hidden-errors")).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    expect(screen.queryByTestId("extension-settings-hidden-errors")).toBeNull();
  });

  it("reports a failed save", async () => {
    vi.mocked(SaveExtensionSettings).mockRejectedValue(new Error("disk full"));
    await renderForm();
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    expect(toastError).toHaveBeenCalledWith("disk full");
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  describe("an extension contributing the command reviewer", () => {
    beforeEach(() => {
      vi.mocked(TestCommandReviewer).mockReset();
    });

    it("saves, then tests its reviewer with the saved settings and shows the result", async () => {
      vi.mocked(TestCommandReviewer).mockResolvedValue({
        mode: "",
        outcome: "fail",
        reason: "invalid_api_key",
      } as never);
      await act(async () => {
        render(<ExtensionSettingsForm extName="jev" reviewer />);
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId("extension-settings-save-and-test"));
      });
      expect(SaveExtensionSettings).toHaveBeenCalled();
      expect(TestCommandReviewer).toHaveBeenCalledWith("jev");
      expect(vi.mocked(SaveExtensionSettings).mock.invocationCallOrder[0]).toBeLessThan(
        vi.mocked(TestCommandReviewer).mock.invocationCallOrder[0]
      );
      expect(screen.getByTestId("extension-settings-test-result")).toHaveTextContent("commandReview.summary.fail");
    });

    it("does not test when the settings do not save", async () => {
      vi.mocked(SaveExtensionSettings).mockResolvedValue([{ field: "baseURL", code: "required" }] as never);
      await act(async () => {
        render(<ExtensionSettingsForm extName="jev" reviewer />);
      });
      await act(async () => {
        fireEvent.click(screen.getByTestId("extension-settings-save-and-test"));
      });
      expect(TestCommandReviewer).not.toHaveBeenCalled();
      expect(screen.queryByTestId("extension-settings-test-result")).toBeNull();
    });

    it("another extension has no test button", async () => {
      await renderForm();
      expect(screen.queryByTestId("extension-settings-save-and-test")).toBeNull();
    });
  });
});
