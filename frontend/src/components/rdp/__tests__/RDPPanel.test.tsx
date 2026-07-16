import { describe, it, expect, beforeAll, beforeEach, vi } from "vitest";
import { render, screen, waitFor, act, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RDPPanel } from "../RDPPanel";
import {
  ConnectRDP,
  SendRDPInput,
  ResizeRDP,
  CloseRDP,
  SetRDPClipboard,
  SetRDPClipboardEnabled,
  SetRDPClipboardFilesFromLocal,
} from "../../../../wailsjs/go/rdp/RDP";
import { ClipboardGetText, ClipboardSetText, EventsOn } from "../../../../wailsjs/runtime/runtime";
import type { asset_entity } from "../../../../wailsjs/go/models";
import { useActiveAssetIds } from "@/hooks/useActiveAssetIds";
import { useTabStore } from "@/stores/tabStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useRDPStore } from "@/stores/rdpStore";

vi.mock("../../../../wailsjs/go/rdp/RDP", () => ({
  ConnectRDP: vi.fn(),
  SendRDPInput: vi.fn().mockResolvedValue(undefined),
  ResizeRDP: vi.fn().mockResolvedValue(undefined),
  CloseRDP: vi.fn().mockResolvedValue(undefined),
  SetRDPClipboard: vi.fn().mockResolvedValue(undefined),
  SetRDPClipboardEnabled: vi.fn().mockResolvedValue(undefined),
  SetRDPClipboardFilesFromLocal: vi.fn().mockResolvedValue(0),
}));

// Radix menus need these DOM APIs happy-dom doesn't implement.
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  Element.prototype.releasePointerCapture = vi.fn();
  // happy-dom has no canvas ImageData; frame tests only need the shape.
  globalThis.ImageData ??= class {
    constructor(
      public data: Uint8ClampedArray,
      public width: number,
      public height: number
    ) {}
  } as unknown as typeof ImageData;
});

interface RDPEvent {
  type: string;
  sessionId: string;
  width?: number;
  height?: number;
  x?: number;
  y?: number;
  rectWidth?: number;
  rectHeight?: number;
  data?: string;
  error?: string;
  text?: string;
  pointerType?: string;
  hotspotX?: number;
  hotspotY?: number;
}

let emit: ((e: RDPEvent) => void) | null;

function asset(cfg: Record<string, unknown> = {}): asset_entity.Asset {
  return {
    ID: 7,
    Name: "win-01",
    Config: JSON.stringify({ host: "192.168.1.10", port: 3389, username: "Administrator", clipboard: true, ...cfg }),
  } as unknown as asset_entity.Asset;
}

beforeEach(() => {
  useTabStore.setState({ tabs: [], activeTabId: null });
  useTerminalStore.setState({ tabData: {}, connections: {}, connectingAssetIds: new Set() });
  useRDPStore.setState({ activeAssetIds: new Set() });
  emit = null;
  vi.mocked(ConnectRDP).mockReset().mockResolvedValue("sess-1");
  vi.mocked(SendRDPInput).mockReset().mockResolvedValue(undefined);
  vi.mocked(ResizeRDP).mockReset().mockResolvedValue(undefined);
  vi.mocked(CloseRDP).mockReset().mockResolvedValue(undefined);
  vi.mocked(SetRDPClipboard).mockReset().mockResolvedValue(undefined);
  vi.mocked(SetRDPClipboardEnabled).mockReset().mockResolvedValue(undefined);
  vi.mocked(SetRDPClipboardFilesFromLocal).mockReset().mockResolvedValue(0);
  vi.mocked(ClipboardGetText).mockReset().mockResolvedValue("");
  vi.mocked(ClipboardSetText).mockReset().mockResolvedValue(true);
  vi.mocked(EventsOn)
    .mockReset()
    .mockImplementation((_event: string, cb: (e: RDPEvent) => void) => {
      emit = cb;
      return () => {};
    });
});

async function renderConnected(props: { onEdit?: () => void } = {}) {
  render(<RDPPanel asset={asset()} onEdit={props.onEdit} />);
  await waitFor(() => expect(emit).toBeTruthy());
  await act(async () => {
    emit!({ type: "connected", sessionId: "sess-1", width: 1280, height: 720 });
  });
}

function ActiveAssetProbe({ assetId }: { assetId: number }) {
  const activeAssetIds = useActiveAssetIds();
  return <span data-testid="active-asset-state">{activeAssetIds.has(assetId) ? "active" : "inactive"}</span>;
}

describe("RDPPanel", () => {
  it("shows malformed persisted RDP configuration instead of connecting with defaults", async () => {
    render(<RDPPanel asset={{ ...asset(), Config: "{" } as asset_entity.Asset} />);

    expect(await screen.findByTestId("rdp-reconnect")).toBeInTheDocument();
    expect(ConnectRDP).not.toHaveBeenCalled();
  });

  it("reports the RDP asset as active while its session is connected", async () => {
    render(
      <>
        <RDPPanel asset={asset()} />
        <ActiveAssetProbe assetId={7} />
      </>
    );

    await waitFor(() => expect(screen.getByTestId("active-asset-state")).toHaveTextContent(/^active$/));

    await act(async () => {
      emit!({ type: "closed", sessionId: "sess-1" });
    });
    expect(screen.getByTestId("active-asset-state")).toHaveTextContent(/^inactive$/);
  });

  it("shows the connection host in the toolbar", async () => {
    render(<RDPPanel asset={asset()} />);
    expect(await screen.findByText("192.168.1.10:3389")).toBeInTheDocument();
  });

  it("connects at the viewport size so the session starts fitted", async () => {
    render(<RDPPanel asset={asset()} />);
    await waitFor(() => expect(ConnectRDP).toHaveBeenCalled());
    const arg = vi.mocked(ConnectRDP).mock.calls[0][0] as { width: number; height: number };
    expect(arg.width).toBe(1200); // viewport width from the test getBoundingClientRect stub
  });

  it("subscribes to the session event channel before starting the RDP connection", async () => {
    render(<RDPPanel asset={asset()} />);

    await waitFor(() => expect(ConnectRDP).toHaveBeenCalledTimes(1));

    expect(EventsOn).toHaveBeenCalledTimes(1);
    expect(vi.mocked(EventsOn).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(ConnectRDP).mock.invocationCallOrder[0]
    );
  });

  it("retries the same viewport resize after a failed resize request", async () => {
    let resizeCallback: ResizeObserverCallback | undefined;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: ResizeObserverCallback) {
          resizeCallback = callback;
        }
        observe() {}
        unobserve() {}
        disconnect() {}
      }
    );
    await renderConnected();
    vi.mocked(ResizeRDP).mockRejectedValueOnce(new Error("resize failed")).mockResolvedValue(undefined);
    const rect = {
      ...document.body.getBoundingClientRect(),
      right: 1000,
      bottom: 700,
      width: 1000,
      height: 700,
    } as DOMRect;
    const rectSpy = vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue(rect);

    act(() => resizeCallback?.([], {} as ResizeObserver));
    await waitFor(() => expect(ResizeRDP).toHaveBeenCalledTimes(1));

    act(() => resizeCallback?.([], {} as ResizeObserver));
    await waitFor(() => expect(ResizeRDP).toHaveBeenCalledTimes(2));
    expect(ResizeRDP).toHaveBeenLastCalledWith("sess-1", 1000, 700);
    rectSpy.mockRestore();
  });

  it("does not resize the remote desktop while its tab is display-none", async () => {
    let resizeCallback: ResizeObserverCallback | undefined;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: ResizeObserverCallback) {
          resizeCallback = callback;
        }
        observe() {}
        unobserve() {}
        disconnect() {}
      }
    );
    await renderConnected();
    vi.mocked(ResizeRDP).mockClear();
    const hiddenRect = {
      ...document.body.getBoundingClientRect(),
      right: 0,
      bottom: 0,
      width: 0,
      height: 0,
    } as DOMRect;
    const rectSpy = vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue(hiddenRect);

    act(() => resizeCallback?.([], {} as ResizeObserver));
    await new Promise((resolve) => setTimeout(resolve, 300));

    expect(ResizeRDP).not.toHaveBeenCalled();
    rectSpy.mockRestore();
  });

  it("syncs the current viewport when switching from 1:1 back to Fit", async () => {
    let resizeCallback: ResizeObserverCallback | undefined;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: ResizeObserverCallback) {
          resizeCallback = callback;
        }
        observe() {}
        unobserve() {}
        disconnect() {}
      }
    );
    await renderConnected();
    vi.mocked(ResizeRDP).mockClear();
    const rect = {
      ...document.body.getBoundingClientRect(),
      right: 1000,
      bottom: 700,
      width: 1000,
      height: 700,
    } as DOMRect;
    const rectSpy = vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue(rect);

    await userEvent.click(screen.getByTestId("rdp-view-actual"));
    act(() => resizeCallback?.([], {} as ResizeObserver));
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(ResizeRDP).not.toHaveBeenCalled();
    await userEvent.click(screen.getByTestId("rdp-view-fit"));

    await waitFor(() => expect(ResizeRDP).toHaveBeenCalledWith("sess-1", 1000, 700));
    rectSpy.mockRestore();
  });

  it("sends the Ctrl+Alt+Del chord in press-then-release order", async () => {
    await renderConnected();
    await userEvent.click(await screen.findByTestId("rdp-special-keys"));
    await userEvent.click(await screen.findByTestId("rdp-key-cad"));
    const keyCalls = vi
      .mocked(SendRDPInput)
      .mock.calls.map((c) => c[0])
      .filter((e) => e.kind === "key");
    expect(keyCalls).toEqual([
      { sessionId: "sess-1", kind: "key", scancode: 0x1d, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x38, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x53, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x53, pressed: false },
      { sessionId: "sess-1", kind: "key", scancode: 0x38, pressed: false },
      { sessionId: "sess-1", kind: "key", scancode: 0x1d, pressed: false },
    ]);
  });

  it("serializes chord key events, awaiting each before sending the next", async () => {
    // Wails dispatches every bound-method call on its own goroutine, so firing the
    // chord's press/release scancodes concurrently lets the remote receive them out
    // of order (e.g. release before press) and the chord silently no-ops. Each key
    // must wait for the previous SendRDPInput to resolve.
    await renderConnected();

    const resolvers: Array<() => void> = [];
    vi.mocked(SendRDPInput).mockClear();
    vi.mocked(SendRDPInput).mockImplementation(() => new Promise<void>((resolve) => resolvers.push(() => resolve())));

    await userEvent.click(await screen.findByTestId("rdp-special-keys"));
    await userEvent.click(await screen.findByTestId("rdp-key-cad"));

    const keyCalls = () => vi.mocked(SendRDPInput).mock.calls.filter((c) => c[0].kind === "key");

    expect(keyCalls()).toHaveLength(1);
    for (let i = 1; i < 6; i++) {
      resolvers[i - 1]();
      await waitFor(() => expect(keyCalls()).toHaveLength(i + 1));
    }
  });

  it("surfaces RDP input failures in the session error overlay", async () => {
    await renderConnected();
    vi.mocked(SendRDPInput).mockRejectedValueOnce(new Error("input transport closed"));

    await userEvent.click(await screen.findByTestId("rdp-special-keys"));
    await userEvent.click(await screen.findByTestId("rdp-key-cad"));

    expect(await screen.findByText(/input transport closed/)).toBeInTheDocument();
    expect(screen.getByTestId("rdp-reconnect")).toBeInTheDocument();
  });

  it("serializes a right-click press and release so the remote cannot receive them out of order", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    const resolvers: Array<() => void> = [];
    vi.mocked(SendRDPInput).mockClear();
    vi.mocked(SendRDPInput).mockImplementation(() => new Promise<void>((resolve) => resolvers.push(resolve)));

    fireEvent.mouseDown(canvas, { button: 2, clientX: 100, clientY: 100 });
    fireEvent.mouseUp(canvas, { button: 2, clientX: 100, clientY: 100 });

    await waitFor(() => expect(SendRDPInput).toHaveBeenCalledTimes(1));
    expect(SendRDPInput).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        sessionId: "sess-1",
        kind: "mouse",
        buttons: 0xa000,
      })
    );

    await act(async () => resolvers[0]());
    await waitFor(() => expect(SendRDPInput).toHaveBeenCalledTimes(2));
    expect(SendRDPInput).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({
        sessionId: "sess-1",
        kind: "mouse",
        buttons: 0x2000,
      })
    );
  });

  it("releases a right mouse button when the pointer is released outside the RDP canvas", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    vi.mocked(SendRDPInput).mockClear();

    fireEvent.mouseDown(canvas, { button: 2, clientX: 100, clientY: 100 });
    fireEvent.mouseUp(window, { button: 2, clientX: 200, clientY: 200 });

    await waitFor(() => expect(SendRDPInput).toHaveBeenCalledTimes(2));
    expect(SendRDPInput).toHaveBeenNthCalledWith(1, expect.objectContaining({ buttons: 0xa000 }));
    expect(SendRDPInput).toHaveBeenNthCalledWith(2, expect.objectContaining({ buttons: 0x2000 }));
  });

  it("releases every held mouse button when the app window loses focus", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    vi.mocked(SendRDPInput).mockClear();

    fireEvent.mouseDown(canvas, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.mouseDown(canvas, { button: 1, clientX: 100, clientY: 100 });
    fireEvent.blur(window);

    await waitFor(() => expect(SendRDPInput).toHaveBeenCalledTimes(4));
    expect(vi.mocked(SendRDPInput).mock.calls.map(([event]) => event.buttons)).toEqual([
      0x9000, 0xc000, 0x1000, 0x4000,
    ]);
  });

  it("forwards a Ctrl+C shortcut to the remote as scancodes, pressed then released", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    canvas.focus();

    fireEvent.keyDown(canvas, { key: "Control", code: "ControlLeft", ctrlKey: true });
    fireEvent.keyDown(canvas, { key: "c", code: "KeyC", ctrlKey: true });
    fireEvent.keyUp(canvas, { key: "c", code: "KeyC", ctrlKey: true });
    fireEvent.keyUp(canvas, { key: "Control", code: "ControlLeft", ctrlKey: false });

    const keyCalls = () =>
      vi
        .mocked(SendRDPInput)
        .mock.calls.map((c) => c[0])
        .filter((e) => e.kind === "key");
    await waitFor(() => expect(keyCalls()).toHaveLength(4));
    expect(keyCalls()).toEqual([
      { sessionId: "sess-1", kind: "key", scancode: 0x1d, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x2e, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x2e, pressed: false },
      { sessionId: "sess-1", kind: "key", scancode: 0x1d, pressed: false },
    ]);
  });

  it("releases a held modifier when the canvas loses focus so it never sticks", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    canvas.focus();

    fireEvent.keyDown(canvas, { key: "Control", code: "ControlLeft", ctrlKey: true });
    fireEvent.blur(canvas);

    const keyCalls = () =>
      vi
        .mocked(SendRDPInput)
        .mock.calls.map((c) => c[0])
        .filter((e) => e.kind === "key");
    await waitFor(() => expect(keyCalls()).toHaveLength(2));
    expect(keyCalls()).toEqual([
      { sessionId: "sess-1", kind: "key", scancode: 0x1d, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x1d, pressed: false },
    ]);
  });

  it("types a plain letter as a scancode press and release so the remote IME can compose", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    canvas.focus();

    fireEvent.keyDown(canvas, { key: "a", code: "KeyA" });
    fireEvent.keyUp(canvas, { key: "a", code: "KeyA" });

    const keyCalls = () =>
      vi
        .mocked(SendRDPInput)
        .mock.calls.map((c) => c[0])
        .filter((e) => e.kind === "key");
    await waitFor(() => expect(keyCalls()).toHaveLength(2));
    expect(keyCalls()).toEqual([
      { sessionId: "sess-1", kind: "key", scancode: 0x1e, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x1e, pressed: false },
    ]);
  });

  it("falls back to a Unicode key event for a printable key with no scancode mapping", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    canvas.focus();

    fireEvent.keyDown(canvas, { key: "5", code: "Numpad5" });

    await waitFor(() =>
      expect(SendRDPInput).toHaveBeenCalledWith({
        sessionId: "sess-1",
        kind: "unicode",
        codepoint: 53,
        pressed: true,
      })
    );
  });

  it("forwards an extended navigation key with the extended flag set", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    canvas.focus();

    fireEvent.keyDown(canvas, { key: "ArrowRight", code: "ArrowRight" });
    fireEvent.keyUp(canvas, { key: "ArrowRight", code: "ArrowRight" });

    const keyCalls = () =>
      vi
        .mocked(SendRDPInput)
        .mock.calls.map((c) => c[0])
        .filter((e) => e.kind === "key");
    await waitFor(() => expect(keyCalls()).toHaveLength(2));
    expect(keyCalls()).toEqual([
      { sessionId: "sess-1", kind: "key", scancode: 0x4d, extended: true, pressed: true },
      { sessionId: "sess-1", kind: "key", scancode: 0x4d, extended: true, pressed: false },
    ]);
  });

  it("reconnects when the error overlay's reconnect button is clicked", async () => {
    render(<RDPPanel asset={asset()} />);
    await waitFor(() => expect(emit).toBeTruthy());
    await act(async () => {
      emit!({ type: "error", sessionId: "sess-1", error: "connection refused" });
    });
    expect(ConnectRDP).toHaveBeenCalledTimes(1);
    await userEvent.click(await screen.findByTestId("rdp-reconnect"));
    await waitFor(() => expect(ConnectRDP).toHaveBeenCalledTimes(2));
  });

  it("invokes onEdit from the error overlay's edit button", async () => {
    const onEdit = vi.fn();
    render(<RDPPanel asset={asset()} onEdit={onEdit} />);
    await waitFor(() => expect(emit).toBeTruthy());
    await act(async () => {
      emit!({ type: "error", sessionId: "sess-1", error: "connection refused" });
    });
    await userEvent.click(await screen.findByTestId("rdp-edit"));
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it("closes the session and offers reconnect when disconnect is clicked", async () => {
    await renderConnected();
    await userEvent.click(await screen.findByTestId("rdp-disconnect"));
    expect(CloseRDP).toHaveBeenCalledWith("sess-1");
    expect(await screen.findByTestId("rdp-reconnect")).toBeInTheDocument();
  });

  it("reflects the clipboard-sync setting from config", async () => {
    render(<RDPPanel asset={asset({ clipboard: true })} />);
    expect(await screen.findByTestId("rdp-clipboard")).toHaveAttribute("data-state", "on");
  });

  it("enters and exits the RDP focus fullscreen mode", async () => {
    await renderConnected();
    const panel = await screen.findByTestId("rdp-panel");
    await userEvent.click(screen.getByTestId("rdp-fullscreen"));
    expect(panel).toHaveAttribute("data-fullscreen", "true");
    expect(panel).toHaveClass("fixed");

    await userEvent.click(screen.getByTestId("rdp-fullscreen"));
    expect(panel).toHaveAttribute("data-fullscreen", "false");
    expect(panel).not.toHaveClass("fixed");
  });

  it("exits the RDP focus fullscreen mode with Escape", async () => {
    await renderConnected();
    const panel = screen.getByTestId("rdp-panel");
    await userEvent.click(screen.getByTestId("rdp-fullscreen"));

    await userEvent.keyboard("{Escape}");

    expect(panel).toHaveAttribute("data-fullscreen", "false");
  });

  it("toggles clipboard synchronization for the active session", async () => {
    await renderConnected();
    const clipboard = screen.getByTestId("rdp-clipboard");

    await userEvent.click(clipboard);

    expect(SetRDPClipboardEnabled).toHaveBeenCalledWith("sess-1", false);
    expect(clipboard).toHaveAttribute("data-state", "off");
  });

  it("sends pasted text to the remote clipboard when synchronization is enabled", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");

    await userEvent.click(canvas);
    await userEvent.paste("hello remote");

    expect(SetRDPClipboard).toHaveBeenCalledWith("sess-1", "hello remote");
    expect(SendRDPInput).toHaveBeenCalledWith({
      sessionId: "sess-1",
      kind: "key",
      scancode: 0x2f,
      pressed: true,
    });
  });

  it("reads the native local clipboard before sending Ctrl+V to the remote desktop", async () => {
    vi.mocked(ClipboardGetText).mockResolvedValue("fresh local text");
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");

    fireEvent.keyDown(canvas, { key: "v", metaKey: true });

    await waitFor(() => expect(SetRDPClipboard).toHaveBeenCalledWith("sess-1", "fresh local text"));
    expect(SendRDPInput).toHaveBeenCalledWith({
      sessionId: "sess-1",
      kind: "key",
      scancode: 0x1d,
      pressed: true,
    });
    expect(SendRDPInput).toHaveBeenCalledWith({
      sessionId: "sess-1",
      kind: "key",
      scancode: 0x2f,
      pressed: true,
    });
    expect(vi.mocked(SetRDPClipboard).mock.invocationCallOrder[0]).toBeLessThan(
      vi.mocked(SendRDPInput).mock.invocationCallOrder[0]
    );
  });

  it("refreshes the remote clipboard before using the remote right-click menu", async () => {
    vi.mocked(ClipboardGetText).mockResolvedValue("fresh context-menu text");
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");

    fireEvent.contextMenu(canvas);

    await waitFor(() => expect(SetRDPClipboard).toHaveBeenCalledWith("sess-1", "fresh context-menu text"));
    expect(SendRDPInput).not.toHaveBeenCalledWith(
      expect.objectContaining({ sessionId: "sess-1", kind: "key", scancode: 0x2f })
    );
  });

  it("offers copied local files before sending Ctrl+V to the remote desktop", async () => {
    vi.mocked(SetRDPClipboardFilesFromLocal).mockResolvedValue(2);
    vi.mocked(ClipboardGetText).mockResolvedValue("stale text");
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");

    fireEvent.keyDown(canvas, { key: "v", metaKey: true });

    await waitFor(() => expect(SetRDPClipboardFilesFromLocal).toHaveBeenCalledWith("sess-1"));
    expect(ClipboardGetText).not.toHaveBeenCalled();
    expect(SetRDPClipboard).not.toHaveBeenCalled();
    expect(SendRDPInput).toHaveBeenCalledWith({
      sessionId: "sess-1",
      kind: "key",
      scancode: 0x2f,
      pressed: true,
    });
  });

  it("does not send pasted text after clipboard synchronization is disabled", async () => {
    await renderConnected();
    await userEvent.click(screen.getByTestId("rdp-clipboard"));
    const canvas = screen.getByTestId("rdp-canvas");

    await userEvent.click(canvas);
    await userEvent.paste("do not send");

    expect(SetRDPClipboard).not.toHaveBeenCalled();
  });

  it("writes remote clipboard text to the native local clipboard", async () => {
    await renderConnected();

    await act(async () => {
      emit!({ type: "clipboard", sessionId: "sess-1", text: "hello local" });
    });

    expect(ClipboardSetText).toHaveBeenCalledWith("hello local");
  });

  it("coalesces mousemove events to at most one send per animation frame", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");
    vi.mocked(SendRDPInput).mockClear();

    fireEvent.mouseMove(canvas, { clientX: 10, clientY: 10 });
    fireEvent.mouseMove(canvas, { clientX: 20, clientY: 20 });
    fireEvent.mouseMove(canvas, { clientX: 30, clientY: 40 });

    await waitFor(() => expect(SendRDPInput).toHaveBeenCalledTimes(1));
    expect(SendRDPInput).toHaveBeenCalledWith(
      expect.objectContaining({ sessionId: "sess-1", kind: "mouse", buttons: 0x0800 })
    );
    // No second flush without new movement.
    await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    expect(SendRDPInput).toHaveBeenCalledTimes(1);
  });

  it("follows remote pointer updates by swapping the canvas cursor", async () => {
    await renderConnected();
    const canvas = screen.getByTestId("rdp-canvas");

    await act(async () => {
      emit!({ type: "pointer", sessionId: "sess-1", pointerType: "hidden" });
    });
    expect(canvas.style.cursor).toBe("none");

    await act(async () => {
      emit!({ type: "pointer", sessionId: "sess-1", pointerType: "default" });
    });
    expect(canvas.style.cursor).toBe("default");
  });

  describe("frame rendering", () => {
    function frameData(byteLength: number): string {
      return btoa(String.fromCharCode(...new Uint8Array(byteLength)));
    }

    function mockCanvas2D() {
      const putImageData = vi.fn();
      const canvas = screen.getByTestId("rdp-canvas") as HTMLCanvasElement;
      canvas.getContext = vi.fn().mockReturnValue({ putImageData }) as unknown as HTMLCanvasElement["getContext"];
      return { canvas, putImageData };
    }

    it("draws an incremental frame region at its rect offset", async () => {
      await renderConnected();
      const { canvas, putImageData } = mockCanvas2D();

      await act(async () => {
        emit!({
          type: "frame",
          sessionId: "sess-1",
          width: 8,
          height: 8,
          x: 3,
          y: 4,
          rectWidth: 2,
          rectHeight: 2,
          data: frameData(2 * 2 * 4),
        });
      });

      expect(canvas.width).toBe(8);
      expect(canvas.height).toBe(8);
      expect(putImageData).toHaveBeenCalledTimes(1);
      const [image, dx, dy] = putImageData.mock.calls[0];
      expect([image.width, image.height, dx, dy]).toEqual([2, 2, 3, 4]);
    });

    it("treats a frame without rect fields as a full frame at the origin", async () => {
      await renderConnected();
      const { canvas, putImageData } = mockCanvas2D();

      await act(async () => {
        emit!({ type: "frame", sessionId: "sess-1", width: 2, height: 2, data: frameData(2 * 2 * 4) });
      });

      expect(canvas.width).toBe(2);
      expect(putImageData).toHaveBeenCalledTimes(1);
      const [image, dx, dy] = putImageData.mock.calls[0];
      expect([image.width, image.height, dx, dy]).toEqual([2, 2, 0, 0]);
    });

    it("drops a frame whose payload does not match its rect size", async () => {
      await renderConnected();
      const { putImageData } = mockCanvas2D();

      await act(async () => {
        emit!({
          type: "frame",
          sessionId: "sess-1",
          width: 8,
          height: 8,
          x: 0,
          y: 0,
          rectWidth: 2,
          rectHeight: 2,
          data: frameData(3), // truncated payload
        });
      });

      expect(putImageData).not.toHaveBeenCalled();
    });
  });
});
