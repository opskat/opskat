import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { asset_entity } from "../../wailsjs/go/models";
import { ConnectRemoteDesktop, EncodeVNCClipboardText } from "../../wailsjs/go/remote_desktop/RemoteDesktop";
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

describe("RemoteDesktopPanel", () => {
  beforeEach(() => {
    approveServer.mockClear();
    FakeRFB.latest = undefined;
    FakeRFB.lastCredentials = undefined;
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
});
