import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

/** Radix DropdownMenuTrigger opens on pointerdown (button=0) under happy-dom. */
function openMenu() {
  fireEvent.pointerDown(screen.getByTestId("linked-asset-menu-trigger"), { button: 0, ctrlKey: false });
}

describe("LinkedAssetControl", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
        },
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("unbound: chip shows the pick placeholder; no asset-library entry", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    expect(screen.getByTestId("linked-asset-menu-trigger")).toHaveTextContent("ai.sidebar.linkedAsset.pickPlaceholder");
    openMenu();
    expect(screen.queryByTestId("menu-pick-library")).not.toBeInTheDocument();
  });

  it("empty state: no open tabs → shows the noOpenTabs row and no tab items", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    expect(screen.getByTestId("menu-no-open-tabs")).toHaveTextContent("ai.sidebar.linkedAsset.noOpenTabs");
    expect(screen.queryByTestId(/^menu-tab-/)).not.toBeInTheDocument();
  });

  it("lists open tabs and binds the one clicked (keyed by tab id, shows tab label)", () => {
    useTabStore.setState({
      tabs: [
        {
          id: "t7",
          type: "terminal",
          label: "web · shell",
          meta: { type: "terminal", assetId: 7, assetName: "web-07" } as any,
        },
      ],
      activeTabId: "t7",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    expect(screen.getByTestId("menu-tab-t7")).toHaveTextContent("web · shell");
    fireEvent.click(screen.getByTestId("menu-tab-t7"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("t7");
    expect(tab?.linkedAssetId).toBe(7);
    expect(tab?.linkedAssetName).toBe("web-07");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("lists two tabs of the SAME asset as two separate items (no dedup by asset)", () => {
    useTabStore.setState({
      tabs: [
        {
          id: "t1",
          type: "terminal",
          label: "prod-web-01",
          meta: { type: "terminal", assetId: 7, assetName: "prod-web-01" } as any,
        },
        {
          id: "t2",
          type: "terminal",
          label: "prod-web-01",
          meta: { type: "terminal", assetId: 7, assetName: "prod-web-01" } as any,
        },
      ],
      activeTabId: "t1",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    expect(screen.getByTestId("menu-tab-t1")).toBeInTheDocument();
    expect(screen.getByTestId("menu-tab-t2")).toBeInTheDocument();
  });

  it("shows the bound chip and clears on 清除绑定", () => {
    useAIStore
      .getState()
      .bindSidebarTab("s1", { workspaceTabId: null, assetId: 42, assetName: "prod-web-01", assetType: "ssh" });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    expect(screen.getByText("prod-web-01")).toBeInTheDocument();
    openMenu();
    fireEvent.click(screen.getByTestId("menu-clear"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
