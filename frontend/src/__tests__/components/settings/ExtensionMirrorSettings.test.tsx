import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ExtensionMirrorSettings } from "../../../components/settings/ExtensionMirrorSettings";
import { GetExtensionMirror, SetExtensionMirror } from "../../../../wailsjs/go/system/System";

async function renderWithStored(stored: string) {
  vi.mocked(GetExtensionMirror).mockResolvedValue(stored);
  const view = render(<ExtensionMirrorSettings />);
  await screen.findByRole("combobox");
  return view;
}

describe("ExtensionMirrorSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(SetExtensionMirror).mockResolvedValue(undefined as never);
  });

  it("saves the preset ghcr.nju.edu.cn host when chosen", async () => {
    const user = userEvent.setup();
    await renderWithStored("");
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "ghcr.nju.edu.cn" }));
    await waitFor(() => expect(SetExtensionMirror).toHaveBeenCalledWith("ghcr.nju.edu.cn"));
  });

  it("saves the preset katch.ggnb.top/ghcr.io mirror when chosen", async () => {
    const user = userEvent.setup();
    await renderWithStored("");
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "katch.ggnb.top/ghcr.io" }));
    await waitFor(() => expect(SetExtensionMirror).toHaveBeenCalledWith("katch.ggnb.top/ghcr.io"));
  });

  it("shows each stored preset as its own option, not as a custom host", async () => {
    for (const preset of ["ghcr.nju.edu.cn", "katch.ggnb.top/ghcr.io"]) {
      const { unmount } = await renderWithStored(preset);
      await waitFor(() => expect(screen.getByRole("combobox")).toHaveTextContent(preset));
      expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("saves an empty host when switching back to direct ghcr.io", async () => {
    const user = userEvent.setup();
    await renderWithStored("ghcr.nju.edu.cn");
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "extension.mirror.direct" }));
    await waitFor(() => expect(SetExtensionMirror).toHaveBeenCalledWith(""));
  });

  it("shows a stored non-preset host as custom in the input", async () => {
    await renderWithStored("registry.example.com:5000");
    expect(screen.getByRole("textbox")).toHaveValue("registry.example.com:5000");
  });

  it("saves a custom host on blur", async () => {
    const user = userEvent.setup();
    await renderWithStored("");
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "extension.mirror.custom" }));
    await user.type(screen.getByRole("textbox"), "registry.example.com:5000");
    await user.tab();
    await waitFor(() => expect(SetExtensionMirror).toHaveBeenCalledWith("registry.example.com:5000"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows the backend rejection on the field when the host is invalid", async () => {
    vi.mocked(SetExtensionMirror).mockRejectedValue(new Error("bad host format"));
    const user = userEvent.setup();
    await renderWithStored("registry.example.com");
    const input = screen.getByRole("textbox");
    await user.clear(input);
    await user.type(input, "https://x.io");
    await user.tab();
    expect(await screen.findByRole("alert")).toHaveTextContent("bad host format");
    expect(input).toHaveAttribute("aria-invalid", "true");
  });

  it("does not call the backend for an empty custom host and asks for one", async () => {
    const user = userEvent.setup();
    await renderWithStored("");
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "extension.mirror.custom" }));
    await user.click(screen.getByRole("textbox"));
    await user.tab();
    expect(await screen.findByRole("alert")).toHaveTextContent("extension.mirror.hostRequired");
    expect(SetExtensionMirror).not.toHaveBeenCalled();
  });
});
