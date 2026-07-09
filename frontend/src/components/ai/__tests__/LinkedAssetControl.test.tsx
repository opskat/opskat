import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore, type Tab } from "@/stores/tabStore";
import { asset_entity } from "../../../../wailsjs/go/models";

/** Radix DropdownMenuTrigger opens on pointerdown (button=0) under happy-dom. */
function openMenu() {
  fireEvent.pointerDown(screen.getByTestId("linked-asset-menu-trigger"), { button: 0, ctrlKey: false });
}

const asset = (id: number, name: string, type: string, icon = "server") =>
  new asset_entity.Asset({ ID: id, Name: name, Type: type, Icon: icon });

const terminalTab = (id: string, label: string, assetId: number, assetName: string): Tab => ({
  id,
  type: "terminal",
  label,
  meta: {
    type: "terminal",
    assetId,
    assetName,
    assetIcon: "server",
    host: "127.0.0.1",
    port: 22,
    username: "root",
  },
});

describe("LinkedAssetControl", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAssetStore.setState({ assets: [asset(42, "prod-web-01", "ssh")] });
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
      tabs: [terminalTab("t7", "web · shell", 7, "web-07")],
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
      tabs: [terminalTab("t1", "prod-web-01", 7, "prod-web-01"), terminalTab("t2", "prod-web-01", 7, "prod-web-01")],
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
