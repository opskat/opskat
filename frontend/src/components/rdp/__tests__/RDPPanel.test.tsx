import { describe, it, expect, beforeAll, beforeEach, vi } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RDPPanel } from "../RDPPanel";
import { ConnectRDP, SendRDPInput, ResizeRDP, CloseRDP } from "../../../../wailsjs/go/rdp/RDP";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import type { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/rdp/RDP", () => ({
  ConnectRDP: vi.fn(),
  SendRDPInput: vi.fn().mockResolvedValue(undefined),
  ResizeRDP: vi.fn().mockResolvedValue(undefined),
  CloseRDP: vi.fn().mockResolvedValue(undefined),
  SetRDPClipboard: vi.fn().mockResolvedValue(undefined),
}));

// Radix menus need these DOM APIs happy-dom doesn't implement.
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  Element.prototype.releasePointerCapture = vi.fn();
});

interface RDPEvent {
  type: string;
  sessionId: string;
  width?: number;
  height?: number;
  error?: string;
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
  emit = null;
  vi.mocked(ConnectRDP).mockReset().mockResolvedValue("sess-1");
  vi.mocked(SendRDPInput).mockReset().mockResolvedValue(undefined);
  vi.mocked(ResizeRDP).mockReset().mockResolvedValue(undefined);
  vi.mocked(CloseRDP).mockReset().mockResolvedValue(undefined);
  vi.mocked(EventsOn).mockReset().mockImplementation((_event: string, cb: (e: RDPEvent) => void) => {
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

describe("RDPPanel", () => {
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
});
