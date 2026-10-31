import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { ExtensionSettingsForm } from "../components/settings/ExtensionSettingsForm";
import { GetExtensionSettings, SaveExtensionSettings } from "../../wailsjs/go/extension/Extension";

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

  it("reports a failed save", async () => {
    vi.mocked(SaveExtensionSettings).mockRejectedValue(new Error("disk full"));
    await renderForm();
    await act(async () => {
      fireEvent.click(screen.getByTestId("extension-settings-save"));
    });
    expect(toastError).toHaveBeenCalledWith("disk full");
    expect(notifySuccess).not.toHaveBeenCalled();
  });
});
