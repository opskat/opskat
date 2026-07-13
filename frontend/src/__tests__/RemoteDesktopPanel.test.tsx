import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { asset_entity } from "../../wailsjs/go/models";
import {
  ConnectRemoteDesktop,
  DisconnectRemoteDesktop,
  EncodeVNCClipboardText,
} from "../../wailsjs/go/remote_desktop/RemoteDesktop";
import { DisconnectSSH, OpenSFTPSession } from "../../wailsjs/go/ssh/SSH";
import { ClipboardGetText, ClipboardSetText } from "../../wailsjs/runtime";
import { RemoteDesktopPanel } from "@/components/remote-desktop/RemoteDesktopPanel";

const approveServer = vi.fn();

class FakeRFB extends EventTarget {
  static lastCredentials: Record<string, string> | undefined;
  static latest: FakeRFB | undefined;
  scaleViewport = true;
  clipViewport = true;
  resizeSession = false;
  background = "";
  _rfbConnectionState = "connecting";

  constructor(_target: HTMLElement, _url: string, options: { credentials: Record<string, string> }) {
    super();
    FakeRFB.lastCredentials = options.credentials;
    FakeRFB.latest = this;
  }

  approveServer = approveServer;
  disconnect = vi.fn();
  clipboardPasteFrom = vi.fn();
  sendKey = vi.fn();
}

vi.mock("@novnc/novnc/lib/rfb", () => ({ default: FakeRFB }));

vi.mock("../../wailsjs/go/remote_desktop/RemoteDesktop", () => ({
  ConnectRemoteDesktop: vi.fn(),
  DisconnectRemoteDesktop: vi.fn(),
  EncodeVNCClipboardText: vi.fn(),
}));

vi.mock("../../wailsjs/go/ssh/SSH", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../wailsjs/go/ssh/SSH")>()),
  DisconnectSSH: vi.fn(),
  OpenSFTPSession: vi.fn(),
}));

// The file-channel panel is exercised by its own tests; stub it here so the
// remote-desktop session-lifecycle tests don't depend on its internals.
vi.mock("@/components/terminal/FileManagerPanel", () => ({
  FileManagerPanel: () => <div data-testid="file-manager" />,
}));

describe("RemoteDesktopPanel", () => {
  beforeEach(() => {
    approveServer.mockClear();
    FakeRFB.latest = undefined;
    FakeRFB.lastCredentials = undefined;
    vi.mocked(DisconnectRemoteDesktop).mockReset();
    vi.mocked(DisconnectSSH).mockReset();
    vi.mocked(OpenSFTPSession).mockReset().mockResolvedValue("sftp-session");
    vi.mocked(ClipboardGetText).mockReset().mockResolvedValue("");
    vi.mocked(ClipboardSetText).mockReset().mockResolvedValue(true);
    vi.mocked(EncodeVNCClipboardText)
      .mockReset()
      .mockImplementation(async (text) =>
        text === "中文" ? [0xd6, 0xd0, 0xce, 0xc4] : [0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a]
      );
    vi.mocked(ConnectRemoteDesktop).mockResolvedValue({
      id: "vnc-session",
      assetId: 1,
      assetType: "vnc",
      assetName: "test-vnc",
      webSocketUrl: "ws://127.0.0.1:12345",
      username: "vnc-user",
      password: "secret",
      fileSshAssetId: 0,
      fileEnabled: false,
      fileStatus: "disabled",
      status: "connecting",
    } as never);
  });

  it("shows the RA2 server identity prompt and continues after approval", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    expect(FakeRFB.lastCredentials).toEqual({ username: "vnc-user", password: "secret" });

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", {
        detail: { type: "RSA", publickey: new Uint8Array([1, 2, 3, 4]) },
      })
    );

    expect(await screen.findByText("remoteDesktop.verifyServerTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("confirm-vnc-server"));
    expect(approveServer).toHaveBeenCalledTimes(1);
  });

  it("decodes UTF-8 clipboard text received from a legacy VNC server", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const wireText = String.fromCharCode(0xe4, 0xb8, 0xad, 0xe6, 0x96, 0x87);
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: wireText } }));

    await waitFor(() => expect(ClipboardSetText).toHaveBeenCalledWith("中文"));
  });

  it("decodes GBK clipboard text received from Chinese Windows VNC", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const wireText = String.fromCharCode(0xd6, 0xd0, 0xce, 0xc4);
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: wireText } }));

    await waitFor(() => expect(ClipboardSetText).toHaveBeenCalledWith("中文"));
  });

  it("sends mixed Chinese and English through one GBK clipboard message", async () => {
    vi.mocked(ClipboardGetText).mockResolvedValue("abc中文XYZ");
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    fireEvent.click(screen.getByText("remoteDesktop.pasteText"));

    await waitFor(() => expect(FakeRFB.latest!.clipboardPasteFrom).toHaveBeenCalledTimes(1));
    const sent = FakeRFB.latest!.clipboardPasteFrom.mock.calls[0][0] as string;
    expect(Array.from(sent, (char) => char.charCodeAt(0))).toEqual([
      0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a,
    ]);
    expect(FakeRFB.latest!.sendKey.mock.calls).toEqual([
      [0xffe3, "ControlLeft", true],
      [0x76, "KeyV", true],
      [0x76, "KeyV", false],
      [0xffe3, "ControlLeft", false],
    ]);
  });

  it("routes the native paste event through Unicode VNC paste", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    const { container } = render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const vncContainer = container.querySelector('[tabindex="0"]');
    expect(vncContainer).not.toBeNull();
    fireEvent.paste(vncContainer!, { clipboardData: { getData: () => "中文" } });

    await waitFor(() => expect(FakeRFB.latest!.clipboardPasteFrom).toHaveBeenCalledTimes(1));
    expect(FakeRFB.latest!.sendKey).toHaveBeenCalledTimes(4);
  });

  it("waits for clipboard encoding before forwarding Ctrl+V to VNC", async () => {
    vi.mocked(ClipboardGetText).mockResolvedValue("abc中文XYZ");
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    const { container } = render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const vncContainer = container.querySelector('[tabindex="0"]');
    fireEvent.keyDown(vncContainer!, { key: "v", code: "KeyV", ctrlKey: true });

    await waitFor(() => expect(FakeRFB.latest!.clipboardPasteFrom).toHaveBeenCalledTimes(1));
    expect(FakeRFB.latest!.sendKey).toHaveBeenCalledTimes(4);
  });

  it("does not forward Ctrl+V when the text was typed directly via keysym fallback", async () => {
    vi.mocked(ClipboardGetText).mockResolvedValue("😀");
    vi.mocked(EncodeVNCClipboardText).mockRejectedValue(new Error("not representable in GBK"));
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    fireEvent.click(screen.getByText("remoteDesktop.pasteText"));

    // The emoji is typed directly as one keysym; the extra Ctrl+V paste must never fire.
    await waitFor(() => expect(FakeRFB.latest!.sendKey).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 40));
    const pressedKeysyms = FakeRFB.latest!.sendKey.mock.calls.map((call) => call[0]);
    expect(pressedKeysyms).not.toContain(0xffe3);
    expect(FakeRFB.latest!.clipboardPasteFrom).not.toHaveBeenCalled();
  });

  it("keeps the live remote desktop session connected when the file panel opens", async () => {
    vi.mocked(ConnectRemoteDesktop).mockResolvedValue({
      id: "vnc-session",
      assetId: 1,
      assetType: "vnc",
      assetName: "test-vnc",
      webSocketUrl: "ws://127.0.0.1:12345",
      username: "vnc-user",
      password: "secret",
      fileSshAssetId: 2,
      fileEnabled: true,
      fileStatus: "enabled",
      status: "connecting",
    } as never);
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<RemoteDesktopPanel tabId="remote-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    fireEvent.click(screen.getByText("remoteDesktop.files"));

    // Wait until the file session has been established and committed (the panel
    // only renders once fileSessionId is set), so the disconnect effects have run.
    await screen.findByTestId("file-manager");
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(OpenSFTPSession).toHaveBeenCalledWith(2);
    expect(DisconnectRemoteDesktop).not.toHaveBeenCalled();
  });
});
