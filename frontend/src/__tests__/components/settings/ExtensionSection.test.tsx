import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { ExtensionSection } from "../../../components/settings/ExtensionSection";
import { ListInstalledExtensions, GetExtensionDetail } from "../../../../wailsjs/go/extension/Extension";

function installed(name: string, credentials: string) {
  return {
    name,
    version: "1.0.0",
    icon: "",
    displayName: name,
    description: "",
    enabled: true,
    manifest: { name, version: "1.0.0", capabilities: { credentials } },
  };
}

// Opens the extension's detail dialog the way a user does: row menu → Details.
async function openDetail(ext: ReturnType<typeof installed>) {
  vi.mocked(ListInstalledExtensions).mockResolvedValue([ext] as never);
  vi.mocked(GetExtensionDetail).mockResolvedValue(ext as never);
  render(<ExtensionSection />);
  await screen.findByText(ext.name);
  // The row menu trigger is the icon-only button; Radix opens it on pointerdown.
  const trigger = screen.getAllByRole("button").find((b) => b.textContent === "")!;
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
  fireEvent.click(await screen.findByText("extension.detail"));
  await waitFor(() => expect(GetExtensionDetail).toHaveBeenCalledWith(ext.name));
  await screen.findByRole("dialog");
}

describe("ExtensionSection detail", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  it("warns that an extension with credentials:read reads stored passwords in plaintext", async () => {
    await openDetail(installed("es", "read"));
    expect(screen.getByRole("alert")).toHaveTextContent("extension.credentialsReadWarning");
  });

  it("shows no credentials warning for an extension without credentials:read", async () => {
    await openDetail(installed("notebook", ""));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
