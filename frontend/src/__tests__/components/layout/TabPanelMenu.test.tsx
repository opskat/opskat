import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import { TabCloseConfirmHost } from "../../../components/layout/TabCloseConfirmHost";
import { TabPanelMenu } from "../../../components/layout/TabPanelMenu";
import { useLayoutStore } from "../../../stores/layoutStore";
import { useTabStore } from "../../../stores/tabStore";
import { useTerminalStore, type TerminalTabData } from "../../../stores/terminalStore";
import { useSFTPStore, type SFTPTransfer } from "../../../stores/sftpStore";

/** Radix DropdownMenuTrigger listens on pointerdown (button=0) to open the menu. */
function openMenu(button: HTMLElement) {
  fireEvent.pointerDown(button, { button: 0, ctrlKey: false });
}

describe("TabPanelMenu", () => {
  beforeEach(() => {
    useLayoutStore.setState({
      tabBarLayout: "top",
      leftPanelWidth: 220,
      leftPanelVisible: true,
      activeSidePanel: "assets",
      filterOpen: false,
    });
    useTabStore.setState({ tabs: [], activeTabId: null });
  });

  afterEach(() => {
    cleanup();
  });

  it("shows 'switch to left' in top mode", () => {
    render(<TabPanelMenu mode="top" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    expect(screen.getByText("sideTabs.switchToLeft")).toBeInTheDocument();
  });

  it("shows 'switch to top' in side mode", () => {
    render(<TabPanelMenu mode="side" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    expect(screen.getByText("sideTabs.switchToTop")).toBeInTheDocument();
  });

  it("clicking 'switch to left' sets layout=left", () => {
    render(<TabPanelMenu mode="top" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    fireEvent.click(screen.getByText("sideTabs.switchToLeft"));
    expect(useLayoutStore.getState().tabBarLayout).toBe("left");
  });

  it("clicking 'switch to top' sets layout=top", () => {
    useLayoutStore.setState({ tabBarLayout: "left" });
    render(<TabPanelMenu mode="side" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    fireEvent.click(screen.getByText("sideTabs.switchToTop"));
    expect(useLayoutStore.getState().tabBarLayout).toBe("top");
  });

  it("clicking 'close all' clears tabs", () => {
    useTabStore.setState({
      tabs: [
        { id: "a", type: "page", label: "a", meta: { type: "page", pageId: "a" } },
        { id: "b", type: "page", label: "b", meta: { type: "page", pageId: "b" } },
      ],
      activeTabId: "a",
    });
    render(<TabPanelMenu mode="top" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    fireEvent.click(screen.getByText("sideTabs.closeAll"));
    expect(useTabStore.getState().tabs).toHaveLength(0);
  });

  it("clicking 'filter' calls onOpenFilter (deferred to escape DropdownMenu close)", async () => {
    const fn = vi.fn();
    render(<TabPanelMenu mode="top" onOpenFilter={fn} />);
    openMenu(screen.getByRole("button"));
    fireEvent.click(screen.getByText("shortcut.panel.filter"));
    await new Promise((r) => setTimeout(r, 0));
    expect(fn).toHaveBeenCalled();
  });

  it("side mode shows panel switch + hide actions", () => {
    render(<TabPanelMenu mode="side" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    expect(screen.getByText("sideTabs.tabsPanel")).toBeInTheDocument();
    expect(screen.getByText("sideTabs.hidePanel")).toBeInTheDocument();
  });

  it("side mode shows reverse switch when current is tabs", () => {
    useLayoutStore.setState({ activeSidePanel: "tabs" });
    render(<TabPanelMenu mode="side" onOpenFilter={vi.fn()} />);
    openMenu(screen.getByRole("button"));
    expect(screen.getByText("sideTabs.assetsPanel")).toBeInTheDocument();
  });
});

describe("TabPanelMenu close confirm", () => {
  const term = (id: string) => ({
    id,
    type: "terminal" as const,
    label: id,
    meta: { type: "terminal" as const, assetId: 1, assetName: id, assetIcon: "", host: "h", port: 22, username: "u" },
  });
  const page = (id: string) => ({
    id,
    type: "page" as const,
    label: id,
    meta: { type: "page" as const, pageId: id },
  });

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
      transfers: {
        x: { transferId: "x", tabId: "t1", status: "active" } as unknown as SFTPTransfer,
      },
    });
    useTabStore.setState({
      tabs: [term("t1"), page("p1")],
      activeTabId: "p1",
      unsavedTabIds: [],
      closeConfirm: null,
    });
  });
  afterEach(() => {
    cleanup();
    useTerminalStore.setState({ tabData: {} });
    useSFTPStore.setState({ transfers: {} });
  });

  it("close all with a live session and transfer asks first, naming both counts", () => {
    render(
      <>
        <TabPanelMenu mode="top" onOpenFilter={vi.fn()} />
        <TabCloseConfirmHost />
      </>
    );
    openMenu(screen.getByRole("button"));
    fireEvent.click(screen.getByText("sideTabs.closeAll"));
    expect(useTabStore.getState().tabs).toHaveLength(2);
    expect(useTabStore.getState().closeConfirm).toMatchObject({ sessions: 1, transfers: 1 });
    fireEvent.click(screen.getByText("tab.closeConfirmAction"));
    expect(useTabStore.getState().tabs).toHaveLength(0);
  });

  it("cancelling keeps every tab", () => {
    render(<TabCloseConfirmHost />);
    act(() => useTabStore.getState().closeOtherTabs("p1"));
    fireEvent.click(screen.getByText("action.cancel"));
    expect(useTabStore.getState().tabs).toHaveLength(2);
    expect(useTabStore.getState().closeConfirm).toBeNull();
  });

  it("single tab with only a live session closes without confirm; with a transfer it asks", () => {
    useSFTPStore.setState({ transfers: {} });
    useTabStore.getState().closeTab("t1");
    expect(useTabStore.getState().closeConfirm).toBeNull();
    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["p1"]);

    useTabStore.setState({ tabs: [term("t1"), page("p1")] });
    useSFTPStore.setState({
      transfers: { x: { transferId: "x", tabId: "t1", status: "active" } as unknown as SFTPTransfer },
    });
    useTabStore.getState().closeTab("t1");
    expect(useTabStore.getState().closeConfirm).toMatchObject({ transfers: 1 });
    expect(useTabStore.getState().tabs).toHaveLength(2);
  });

  it("unsaved editor guard still applies after confirm, without a second dialog", () => {
    useTabStore.setState({ unsavedTabIds: ["p1"] });
    useTabStore.getState().closeAllTabs();
    expect(useTabStore.getState().closeConfirm).not.toBeNull();
    useTabStore.getState().confirmClose();
    expect(useTabStore.getState().closeConfirm).toBeNull();
    expect(useTabStore.getState().pendingCloseTabId).toBe("p1");
  });
});
