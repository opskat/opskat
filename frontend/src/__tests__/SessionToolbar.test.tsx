import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SessionToolbar } from "@/components/terminal/SessionToolbar";
import { useTerminalStore, type SplitNode } from "@/stores/terminalStore";
import { useTabStore } from "@/stores/tabStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";

const TAB = "tab1";

function setup(paneIds: string[], activePaneId = paneIds[0]) {
  const closePane = vi.fn();
  const splitPane = vi.fn();
  const reconnect = vi.fn();
  const setActivePaneId = vi.fn();

  const panes = Object.fromEntries(
    paneIds.map((id) => [id, { sessionId: id, transport: "ssh" as const, connected: true, connectedAt: 0 }])
  );
  const splitTree: SplitNode =
    paneIds.length > 1
      ? {
          type: "split",
          direction: "vertical",
          ratio: 0.5,
          first: { type: "terminal", sessionId: paneIds[0] },
          second: { type: "terminal", sessionId: paneIds[1] },
        }
      : { type: "terminal", sessionId: paneIds[0] };

  useTerminalStore.setState({
    tabData: { [TAB]: { splitTree, activePaneId, panes, directoryFollowMode: "off" } },
    closePane,
    splitPane,
    reconnect,
    setActivePaneId,
  });
  useTabStore.setState({
    tabs: [
      {
        id: TAB,
        type: "terminal",
        label: "web-01",
        meta: {
          type: "terminal",
          assetId: 1,
          assetName: "web-01",
          assetIcon: "",
          host: "web-01",
          port: 22,
          username: "root",
        },
      },
    ],
    activeTabId: TAB,
  });

  return { closePane, splitPane, reconnect, setActivePaneId };
}

beforeEach(() => {
  useTerminalThemeStore.setState({ toolbarPinned: true });
});

describe("SessionToolbar (per-pane)", () => {
  it("shows the close button only when the tab is split, and closes that specific pane", () => {
    const { closePane } = setup(["s1", "s2"]);
    render(<SessionToolbar tabId={TAB} sessionId="s2" />);

    const closeBtn = screen.getByTitle("ssh.contextMenu.closePane");
    fireEvent.click(closeBtn);
    expect(closePane).toHaveBeenCalledWith(TAB, "s2");
  });

  it("hides the close button when there is only a single pane", () => {
    setup(["only"]);
    render(<SessionToolbar tabId={TAB} sessionId="only" />);
    expect(screen.queryByTitle("ssh.contextMenu.closePane")).toBeNull();
  });

  it("pin toggle flips the global toolbarPinned preference", () => {
    setup(["s1"]);
    render(<SessionToolbar tabId={TAB} sessionId="s1" />);

    // Currently pinned → the button offers to auto-hide.
    fireEvent.click(screen.getByTitle("ssh.session.autoHideToolbar"));
    expect(useTerminalThemeStore.getState().toolbarPinned).toBe(false);
  });

  it("splitting focuses the owning pane first, then splits", () => {
    const { splitPane, setActivePaneId } = setup(["s1", "s2"], "s1");
    render(<SessionToolbar tabId={TAB} sessionId="s2" />);

    fireEvent.click(screen.getByTitle("ssh.session.splitV"));
    expect(setActivePaneId).toHaveBeenCalledWith(TAB, "s2");
    expect(splitPane).toHaveBeenCalledWith(TAB, "vertical");
  });
});
