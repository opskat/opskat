import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, act, cleanup, waitFor } from "@testing-library/react";
import { TabCloseConfirmHost } from "@/components/layout/TabCloseConfirmHost";
import { useTabStore, type Tab } from "@/stores/tabStore";
import { useSFTPStore, type SFTPTransfer } from "@/stores/sftpStore";
import { TRANSPORTS, useTerminalStore, type TerminalTabData } from "@/stores/terminalStore";
import { VNCPanel } from "@/components/vnc/VNCPanel";
import { ConnectVNC } from "../../../../wailsjs/go/vnc/VNC";
import type { asset_entity } from "../../../../wailsjs/go/models";

const onConnected: { current: (() => void) | null } = { current: null };

vi.mock("../../../../wailsjs/go/vnc/VNC", () => ({
  CheckVNCServerKey: vi.fn(),
  ConnectVNC: vi.fn(),
  DisconnectVNC: vi.fn().mockResolvedValue(undefined),
  EncodeVNCClipboardText: vi.fn(),
  StartVNCStream: vi.fn().mockResolvedValue(undefined),
  TrustVNCServerKey: vi.fn(),
}));

vi.mock("@/lib/vncClient", () => ({
  startVNCClient: (opts: { onConnected: () => void }) => {
    onConnected.current = opts.onConnected;
    return { rfb: {}, result: new Promise(() => {}), cleanup: vi.fn() };
  },
  vncClientFailureMessageKey: () => "vnc.failure",
}));

const term: Tab = {
  id: "t1",
  type: "terminal",
  label: "t1",
  meta: { type: "terminal", assetId: 1, assetName: "t1", assetIcon: "", host: "h", port: 22, username: "u" },
} as Tab;

const vncTab: Tab = {
  id: "v1",
  type: "page",
  label: "desk",
  meta: { type: "page", pageId: "vnc", assetId: 9 },
} as Tab;

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  useTerminalStore.setState({ tabData: {} });
  useSFTPStore.setState({ transfers: {} });
  useTabStore.setState({ tabs: [], activeTabId: null, closeConfirm: null });
});

describe("closing the last terminal pane with an in-flight transfer", () => {
  beforeEach(() => {
    useTerminalStore.setState({
      tabData: {
        t1: {
          splitTree: { type: "terminal", sessionId: "s1" },
          activePaneId: "s1",
          panes: { s1: { sessionId: "s1", connected: true, connectedAt: 0, transport: "ssh" } },
        } as unknown as TerminalTabData,
      },
    });
    useSFTPStore.setState({
      transfers: { x: { transferId: "x", tabId: "t1", status: "active" } as unknown as SFTPTransfer },
    });
    useTabStore.setState({ tabs: [term], activeTabId: "t1", unsavedTabIds: [], closeConfirm: null });
  });

  it("asks before disconnecting, and cancelling keeps the session connected", () => {
    const disconnect = vi.spyOn(TRANSPORTS.ssh, "disconnect").mockImplementation(() => {});
    render(<TabCloseConfirmHost />);
    act(() => useTerminalStore.getState().closePane("t1", "s1"));
    expect(screen.getByText("tab.closeConfirmAction")).toBeInTheDocument();
    expect(disconnect).not.toHaveBeenCalled();

    act(() => useTabStore.getState().cancelCloseConfirm());
    expect(disconnect).not.toHaveBeenCalled();
    expect(useTabStore.getState().tabs).toHaveLength(1);
    expect(useTerminalStore.getState().tabData.t1.panes.s1.connected).toBe(true);
  });
});

describe("bulk close counts connected VNC sessions", () => {
  it("confirms closing all tabs while a VNC session is connected", async () => {
    vi.mocked(ConnectVNC).mockResolvedValue({ id: "vs1", fileSshAssetId: 0 } as never);
    const asset = { ID: 9, Name: "desk", Config: JSON.stringify({ host: "10.0.0.9", port: 5900 }) };
    useTabStore.setState({ tabs: [vncTab], activeTabId: "v1", unsavedTabIds: [], closeConfirm: null });
    render(
      <>
        <VNCPanel tabId="v1" asset={asset as unknown as asset_entity.Asset} />
        <TabCloseConfirmHost />
      </>
    );
    await waitFor(() => expect(onConnected.current).toBeTruthy());
    act(() => onConnected.current!());

    act(() => useTabStore.getState().closeAllTabs());
    expect(useTabStore.getState().closeConfirm).toMatchObject({ sessions: 1 });
    expect(useTabStore.getState().tabs).toHaveLength(1);
  });
});
