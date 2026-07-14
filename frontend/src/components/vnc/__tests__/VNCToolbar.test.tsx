import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";
import { VNCToolbar } from "@/components/vnc/VNCToolbar";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (k: string) => k, i18n: { language: "en" } }),
}));

// Radix menus need these DOM APIs happy-dom doesn't implement (see RDPPanel.test.tsx).
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  Element.prototype.releasePointerCapture = vi.fn();
});

function setup(overrides: Partial<React.ComponentProps<typeof VNCToolbar>> = {}) {
  const props = {
    assetName: "desk-01",
    host: "10.0.0.1",
    port: 5901,
    status: "connected" as const,
    statusLabel: "Connected",
    viewMode: "fit" as const,
    clipboardEnabled: true,
    filesEnabled: true,
    filesOpen: false,
    isFullscreen: false,
    onViewModeChange: vi.fn(),
    onSendSpecialKey: vi.fn(),
    onToggleClipboard: vi.fn(),
    onToggleFiles: vi.fn(),
    onToggleFullscreen: vi.fn(),
    onDisconnect: vi.fn(),
    ...overrides,
  };
  render(<VNCToolbar {...props} />);
  return props;
}

describe("VNCToolbar", () => {
  it("renders identity, host chip and status pill", () => {
    setup();
    expect(screen.getByText("desk-01")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.1:5901")).toBeInTheDocument();
    expect(screen.getByTestId("vnc-status")).toHaveTextContent("Connected");
  });
  it("fires view-mode change", () => {
    const p = setup();
    fireEvent.click(screen.getByTestId("vnc-view-original"));
    expect(p.onViewModeChange).toHaveBeenCalledWith("original");
  });
  it("sends Ctrl+Alt+Del from the special-keys menu", async () => {
    const p = setup();
    // Radix's DropdownMenuTrigger opens on pointerdown; fireEvent.click alone doesn't
    // synthesize that, so use userEvent here (same as RDPPanel.test.tsx).
    await userEvent.click(screen.getByTestId("vnc-special-keys"));
    await userEvent.click(await screen.findByTestId("vnc-key-cad"));
    expect(p.onSendSpecialKey).toHaveBeenCalledWith("ctrl-alt-del");
  });
  it("disables special keys / clipboard / disconnect when not connected", () => {
    setup({ status: "closed" });
    expect(screen.getByTestId("vnc-special-keys")).toBeDisabled();
    expect(screen.getByTestId("vnc-clipboard")).toBeDisabled();
    expect(screen.getByTestId("vnc-disconnect")).toBeDisabled();
  });
  it("disables the files button when there is no channel", () => {
    setup({ filesEnabled: false });
    expect(screen.getByTestId("vnc-files")).toBeDisabled();
  });
  it("reflects clipboard state via data-state", () => {
    setup({ clipboardEnabled: false });
    expect(screen.getByTestId("vnc-clipboard")).toHaveAttribute("data-state", "off");
  });
});
