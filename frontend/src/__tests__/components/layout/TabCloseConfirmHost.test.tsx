import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import { TabCloseConfirmHost } from "../../../components/layout/TabCloseConfirmHost";
import { useTabStore } from "../../../stores/tabStore";
import { useSFTPStore, type SFTPTransfer } from "../../../stores/sftpStore";
import { useTerminalStore, type TerminalTabData } from "../../../stores/terminalStore";

const term = {
  id: "t1",
  type: "terminal" as const,
  label: "t1",
  meta: { type: "terminal" as const, assetId: 1, assetName: "t1", assetIcon: "", host: "h", port: 22, username: "u" },
};

describe("TabCloseConfirmHost (no tab-bar menu mounted)", () => {
  beforeEach(() => {
    useTerminalStore.setState({
      tabData: {
        t1: {
          splitTree: { type: "leaf", sessionId: "s1" },
          panes: { p: { sessionId: "s1", connected: true, connectedAt: 0, transport: "ssh" } },
        } as unknown as TerminalTabData,
      },
    });
    useSFTPStore.setState({
      transfers: { x: { transferId: "x", tabId: "t1", status: "active" } as unknown as SFTPTransfer },
    });
    useTabStore.setState({ tabs: [term], activeTabId: "t1", unsavedTabIds: [], closeConfirm: null });
  });
  afterEach(() => {
    cleanup();
    useTerminalStore.setState({ tabData: {} });
    useSFTPStore.setState({ transfers: {} });
  });

  it("shows the confirm dialog when closing a tab with an in-flight transfer, and closes on confirm", () => {
    render(<TabCloseConfirmHost />);
    act(() => useTabStore.getState().closeTab("t1"));
    expect(screen.getByText("tab.closeConfirmAction")).toBeInTheDocument();
    fireEvent.click(screen.getByText("tab.closeConfirmAction"));
    expect(useTabStore.getState().tabs).toHaveLength(0);
  });
});
