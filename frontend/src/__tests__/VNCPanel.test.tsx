import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { asset_entity } from "../../wailsjs/go/models";
import {
  CheckVNCServerKey,
  ConnectVNC,
  DisconnectVNC,
  EncodeVNCClipboardText,
  StartVNCStream,
  TrustVNCServerKey,
} from "../../wailsjs/go/vnc/VNC";
import { DisconnectSSH, OpenSFTPSession } from "../../wailsjs/go/ssh/SSH";
import { ClipboardGetText, ClipboardSetText } from "../../wailsjs/runtime";
import { VNCPanel } from "@/components/vnc/VNCPanel";
import { toast } from "sonner";

const { approveServer, FakeRFB } = vi.hoisted(() => {
  const approveServer = vi.fn();
  class FakeRFB extends EventTarget {
    static lastOptions: { credentials?: Record<string, string>; securityPolicy?: number[][] } | undefined;
    static latest: FakeRFB | undefined;
    static constructorError: Error | undefined;
    scaleViewport = true;
    clipViewport = true;
    resizeSession = false;
    background = "";
    _rfbConnectionState = "connecting";

    constructor(
      _target: HTMLElement,
      _url: string,
      options?: { credentials?: Record<string, string>; securityPolicy?: number[][] }
    ) {
      super();
      if (FakeRFB.constructorError) throw FakeRFB.constructorError;
      FakeRFB.lastOptions = options;
      FakeRFB.latest = this;
    }

    approveServer = approveServer;
    sendCredentials = vi.fn();
    disconnect = vi.fn();
    clipboardPasteFrom = vi.fn();
    sendKey = vi.fn();
    sendCtrlAltDel = vi.fn();
  }
  return { approveServer, FakeRFB };
});

// Local override of the global react-i18next mock (setup.ts): that mock keeps
// `t` a stable reference on purpose, but this file needs to flip `t`'s
// identity between renders to reproduce a `languageChanged` event. Every
// other test in this file still sees a `t` that just echoes its key.
let currentT: (key: string) => string = (key: string) => key;
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: currentT, i18n: { language: "en", changeLanguage: vi.fn() } }),
  Trans: ({ i18nKey, children }: { i18nKey?: string; children?: React.ReactNode }) => i18nKey ?? children,
  initReactI18next: { type: "3rdParty", init: vi.fn() },
}));

vi.mock("@novnc/novnc", () => ({ default: FakeRFB }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

// Radix menus need these DOM APIs happy-dom doesn't implement (see RDPPanel.test.tsx).
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  Element.prototype.releasePointerCapture = vi.fn();
});

vi.mock("../../wailsjs/go/vnc/VNC", () => ({
  CheckVNCServerKey: vi.fn(),
  ConnectVNC: vi.fn(),
  DisconnectVNC: vi.fn(),
  EncodeVNCClipboardText: vi.fn(),
  StartVNCStream: vi.fn(),
  TrustVNCServerKey: vi.fn(),
  WriteVNC: vi.fn(),
}));

vi.mock("../../wailsjs/go/ssh/SSH", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../wailsjs/go/ssh/SSH")>()),
  DisconnectSSH: vi.fn(),
  OpenSFTPSession: vi.fn(),
}));

// The file-channel panel is exercised by its own tests; stub it here so the
// VNC session-lifecycle tests don't depend on its internals.
vi.mock("@/components/terminal/FileManagerPanel", () => ({
  FileManagerPanel: () => <div data-testid="file-manager" />,
}));

function dispatchConnected() {
  FakeRFB.latest!.dispatchEvent(
    new CustomEvent("negotiatedsecurity", {
      detail: { type: 2, name: "VNCAuth", authenticationEncrypted: false, sessionEncrypted: false },
    })
  );
  FakeRFB.latest!.dispatchEvent(new CustomEvent("connect"));
}

describe("VNCPanel", () => {
  beforeEach(() => {
    currentT = (key: string) => key;
    approveServer.mockClear();
    FakeRFB.latest = undefined;
    FakeRFB.lastOptions = undefined;
    FakeRFB.constructorError = undefined;
    vi.mocked(CheckVNCServerKey)
      .mockReset()
      .mockResolvedValue({
        state: "first_use",
        host: "vnc.example.com",
        port: 5901,
        newFingerprint: "SHA256:new-vnc-key",
      } as never);
    vi.mocked(TrustVNCServerKey)
      .mockReset()
      .mockResolvedValue(undefined as never);
    vi.mocked(DisconnectVNC).mockReset();
    vi.mocked(StartVNCStream)
      .mockReset()
      .mockResolvedValue(undefined as never);
    vi.mocked(DisconnectSSH).mockReset();
    vi.mocked(OpenSFTPSession).mockReset().mockResolvedValue("sftp-session");
    vi.mocked(ClipboardGetText).mockReset().mockResolvedValue("");
    vi.mocked(ClipboardSetText).mockReset().mockResolvedValue(true);
    vi.mocked(toast.error).mockReset();
    vi.mocked(EncodeVNCClipboardText)
      .mockReset()
      .mockImplementation(async (text) =>
        text === "中文" ? [0xd6, 0xd0, 0xce, 0xc4] : [0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a]
      );
    vi.mocked(ConnectVNC).mockResolvedValue({
      id: "vnc-session",
      username: "vnc-user",
      password: "secret",
      fileSshAssetId: 0,
    } as never);
  });

  it("durably trusts the RA2 server before approval and only then supplies credentials", async () => {
    const order: string[] = [];
    vi.mocked(CheckVNCServerKey).mockImplementation(async () => {
      order.push("check");
      return {
        state: "first_use",
        host: "vnc.example.com",
        port: 5901,
        newFingerprint: "SHA256:new-vnc-key",
      } as never;
    });
    vi.mocked(TrustVNCServerKey).mockImplementation(async () => {
      order.push("trust");
    });
    approveServer.mockImplementation(() => order.push("approve"));
    const asset = new asset_entity.Asset({
      ID: 1,
      Name: "test-vnc",
      Type: "vnc",
      Config: JSON.stringify({ host: "vnc.example.com", port: 5901, encryption: "always_maximum" }),
    });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    expect(StartVNCStream).toHaveBeenCalledWith("vnc-session");
    expect(FakeRFB.lastOptions).toEqual({ securityPolicy: [[129, 133]] });
    expect(FakeRFB.latest!.sendCredentials).not.toHaveBeenCalled();

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", {
        detail: { type: "RSA", publickey: new Uint8Array([1, 2, 3, 4]) },
      })
    );

    expect(await screen.findByText("vnc.verifyServerTitle")).toBeInTheDocument();
    expect(
      screen.getAllByText("vnc.example.com:5901").some((node) => node.closest("div")?.classList.contains("select-text"))
    ).toBe(true);
    expect(screen.getByText("SHA256:new-vnc-key")).toHaveClass("select-text");
    expect(CheckVNCServerKey).toHaveBeenCalledWith("vnc-session", "AQIDBA==");
    expect(FakeRFB.latest!.sendCredentials).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId("vnc-verify-trust"));
    await waitFor(() => expect(approveServer).toHaveBeenCalledTimes(1));
    expect(TrustVNCServerKey).toHaveBeenCalledWith("vnc-session", "AQIDBA==", false);
    expect(order).toEqual(["check", "trust", "approve"]);

    FakeRFB.latest!.dispatchEvent(new CustomEvent("credentialsrequired", { detail: { types: ["password"] } }));
    expect(FakeRFB.latest!.sendCredentials).toHaveBeenCalledWith({ username: "vnc-user", password: "secret" });
  });

  it("disconnects a backend session that resolves after the panel unmounts", async () => {
    let resolveConnect!: (session: unknown) => void;
    vi.mocked(ConnectVNC).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveConnect = resolve;
        }) as never
    );
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc", Config: "{}" });
    const { unmount } = render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(ConnectVNC).toHaveBeenCalledWith(1));
    unmount();
    resolveConnect({ id: "late-session", fileSshAssetId: 0 });

    await waitFor(() => expect(DisconnectVNC).toHaveBeenCalledWith("late-session"));
    expect(StartVNCStream).not.toHaveBeenCalled();
  });

  it("auto-approves an exact durable key match without prompting", async () => {
    vi.mocked(CheckVNCServerKey).mockResolvedValue({
      state: "match",
      host: "vnc.example.com",
      port: 5901,
      newFingerprint: "SHA256:trusted-vnc-key",
    } as never);
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc", Config: "{}" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", { detail: { publickey: new Uint8Array([1, 2, 3, 4]) } })
    );

    await waitFor(() => expect(approveServer).toHaveBeenCalledTimes(1));
    expect(TrustVNCServerKey).not.toHaveBeenCalled();
    expect(screen.queryByText("vnc.verifyServerTitle")).not.toBeInTheDocument();
  });

  it.each([
    [
      { type: 129, name: "RA2_256", authenticationEncrypted: true, sessionEncrypted: true, aesBits: 256 },
      "vnc.security.sessionEncrypted",
      "text-info",
    ],
    [
      { type: 130, name: "RA2ne_256", authenticationEncrypted: true, sessionEncrypted: false, aesBits: 256 },
      "vnc.security.authenticationOnly",
      "text-warning",
    ],
    [
      { type: 2, name: "VNCAuth", authenticationEncrypted: false, sessionEncrypted: false },
      "vnc.security.unencrypted",
      "text-warning",
    ],
  ])("renders actual negotiated protection instead of the configured preference", async (detail, key, tone) => {
    const asset = new asset_entity.Asset({
      ID: 1,
      Name: "test-vnc",
      Type: "vnc",
      Config: JSON.stringify({ encryption: "always_maximum" }),
    });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!.dispatchEvent(new CustomEvent("negotiatedsecurity", { detail }));
    FakeRFB.latest!.dispatchEvent(new CustomEvent("connect"));

    expect(await screen.findByText(detail.name)).toBeInTheDocument();
    expect(screen.getByText(key)).toHaveClass(tone);
  });

  it("closes the backend session when noVNC construction fails", async () => {
    FakeRFB.constructorError = new Error("invalid channel");
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc", Config: "{}" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(screen.getByText("Error: invalid channel")).toBeInTheDocument());
    expect(DisconnectVNC).toHaveBeenCalledWith("vnc-session");
  });

  it("maps an unsatisfied credential request to its own localized action", async () => {
    vi.mocked(ConnectVNC).mockResolvedValue({
      id: "vnc-session",
      username: "vnc-user",
      fileSshAssetId: 0,
    } as never);
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc", Config: "{}" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!.dispatchEvent(new CustomEvent("credentialsrequired", { detail: { types: ["password"] } }));

    expect(await screen.findByText("vnc.failure.unsatisfiedCredentials")).toBeInTheDocument();
    expect(FakeRFB.latest!.sendCredentials).not.toHaveBeenCalled();
  });

  it.each([
    ["policy-rejected", "vnc.failure.policyRejected"],
    ["unsupported-security-type", "vnc.failure.unsupportedSecurityType"],
    ["authentication-failed", "vnc.failure.authenticationFailed"],
    ["integrity-failed", "vnc.failure.integrityFailed"],
    ["transport-closed", "vnc.failure.transportClosed"],
  ])("maps typed %s failures without parsing private messages", async (code, expectedMessage) => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc", Config: "{}" });
    const { unmount } = render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("connectionfailure", { detail: { code, message: "opaque protocol message" } })
    );

    expect(await screen.findByText(expectedMessage)).toBeInTheDocument();
    expect(screen.queryByText("opaque protocol message")).not.toBeInTheDocument();
    unmount();
  });

  it("decodes UTF-8 clipboard text received from a legacy VNC server", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const wireText = String.fromCharCode(0xe4, 0xb8, 0xad, 0xe6, 0x96, 0x87);
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: wireText } }));

    await waitFor(() => expect(ClipboardSetText).toHaveBeenCalledWith("中文"));
  });

  it("decodes GBK clipboard text received from Chinese Windows VNC", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const wireText = String.fromCharCode(0xd6, 0xd0, 0xce, 0xc4);
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: wireText } }));

    await waitFor(() => expect(ClipboardSetText).toHaveBeenCalledWith("中文"));
  });

  it("surfaces local clipboard write failures", async () => {
    vi.mocked(ClipboardSetText).mockRejectedValue(new Error("clipboard unavailable"));
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: "hello" } }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: clipboard unavailable"));
  });

  it("sends mixed Chinese and English through one GBK clipboard message", async () => {
    vi.mocked(ClipboardGetText).mockResolvedValue("abc中文XYZ");
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const vncContainer = document.querySelector('[tabindex="0"]')!;
    fireEvent.keyDown(vncContainer, { key: "v", code: "KeyV", ctrlKey: true });

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
    const { container } = render(<VNCPanel tabId="vnc-1" asset={asset} />);

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
    const { container } = render(<VNCPanel tabId="vnc-1" asset={asset} />);

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
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const vncContainer = document.querySelector('[tabindex="0"]')!;
    fireEvent.keyDown(vncContainer, { key: "v", code: "KeyV", ctrlKey: true });

    // The emoji is typed directly as one keysym; the extra Ctrl+V paste must never fire.
    await waitFor(() => expect(FakeRFB.latest!.sendKey).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 40));
    const pressedKeysyms = FakeRFB.latest!.sendKey.mock.calls.map((call) => call[0]);
    expect(pressedKeysyms).not.toContain(0xffe3);
    expect(FakeRFB.latest!.clipboardPasteFrom).not.toHaveBeenCalled();
  });

  it("keeps the live remote desktop session connected when the file panel opens", async () => {
    vi.mocked(ConnectVNC).mockResolvedValue({
      id: "vnc-session",
      username: "vnc-user",
      password: "secret",
      fileSshAssetId: 2,
    } as never);
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    fireEvent.click(screen.getByTestId("vnc-files"));

    // Wait until the file session has been established and committed (the panel
    // only renders once fileSessionId is set), so the disconnect effects have run.
    await screen.findByTestId("file-manager");
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(OpenSFTPSession).toHaveBeenCalledWith(2);
    expect(DisconnectVNC).not.toHaveBeenCalled();
  });

  it("keeps the live VNC transport connected across a UI language change", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    const { rerender } = render(<VNCPanel tabId="vnc-1" asset={asset} />);

    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    const rfbBeforeLanguageChange = FakeRFB.latest;
    expect(StartVNCStream).toHaveBeenCalledTimes(1);

    // Simulate react-i18next firing `languageChanged`: every consumer of
    // useTranslation() re-renders with a brand-new `t` reference.
    currentT = (key: string) => key;
    rerender(<VNCPanel tabId="vnc-1" asset={asset} />);
    await new Promise((resolve) => setTimeout(resolve, 0));

    // The transport effect must not have torn down and rebuilt: same noVNC
    // instance, no disconnect(), and StartVNCStream not re-invoked
    // (the backend pump is startOnce-guarded, so a second call would desync
    // the fresh noVNC instance mid-stream).
    expect(FakeRFB.latest).toBe(rfbBeforeLanguageChange);
    expect(rfbBeforeLanguageChange!.disconnect).not.toHaveBeenCalled();
    expect(StartVNCStream).toHaveBeenCalledTimes(1);
  });

  it("sends Ctrl+Alt+Del through noVNC's built-in helper", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);
    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!._rfbConnectionState = "connected";
    dispatchConnected();
    // The special-keys trigger is a Radix DropdownMenu: fireEvent.click doesn't open it
    // in happy-dom, so drive it with userEvent (same as VNCToolbar/RDPPanel tests). Wait
    // for the trigger to become enabled once the connect event flips status to connected.
    const trigger = await screen.findByTestId("vnc-special-keys");
    await waitFor(() => expect(trigger).toBeEnabled());
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByTestId("vnc-key-cad"));
    expect(FakeRFB.latest!.sendCtrlAltDel).toHaveBeenCalledTimes(1);
  });

  it("sends Alt+Tab as an ordered keysym press/release sequence", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);
    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!._rfbConnectionState = "connected";
    dispatchConnected();
    const trigger = await screen.findByTestId("vnc-special-keys");
    await waitFor(() => expect(trigger).toBeEnabled());
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByTestId("vnc-key-alt-tab"));
    // Alt_L down, Tab down, Tab up, Alt_L up — order matters so Alt never sticks down remotely.
    expect(FakeRFB.latest!.sendKey.mock.calls).toEqual([
      [0xffe9, "AltLeft", true],
      [0xff09, "Tab", true],
      [0xff09, "Tab", false],
      [0xffe9, "AltLeft", false],
    ]);
  });

  it("sends Esc as a single keysym press then release", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);
    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!._rfbConnectionState = "connected";
    dispatchConnected();
    const trigger = await screen.findByTestId("vnc-special-keys");
    await waitFor(() => expect(trigger).toBeEnabled());
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByTestId("vnc-key-esc"));
    expect(FakeRFB.latest!.sendKey.mock.calls).toEqual([
      [0xff1b, "Escape", true],
      [0xff1b, "Escape", false],
    ]);
  });

  it("stops mirroring the remote clipboard when sync is turned off", async () => {
    const asset = new asset_entity.Asset({ ID: 1, Name: "test-vnc", Type: "vnc" });
    render(<VNCPanel tabId="vnc-1" asset={asset} />);
    await waitFor(() => expect(FakeRFB.latest).toBeDefined());
    FakeRFB.latest!._rfbConnectionState = "connected";
    dispatchConnected();
    // The clipboard toggle is a plain button (not a Radix menu), so fireEvent.click is fine;
    // wait for it to become enabled once the session is connected.
    const clipboard = await screen.findByTestId("vnc-clipboard");
    await waitFor(() => expect(clipboard).toBeEnabled());
    fireEvent.click(clipboard); // turn off
    FakeRFB.latest!.dispatchEvent(new CustomEvent("clipboard", { detail: { text: "hello" } }));
    await new Promise((r) => setTimeout(r, 20));
    expect(ClipboardSetText).not.toHaveBeenCalled();
  });
});
