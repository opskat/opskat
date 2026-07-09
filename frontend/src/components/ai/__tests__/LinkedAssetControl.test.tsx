import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: ({ onValueChange }: { onValueChange: (id: number) => void }) => (
    <button data-testid="asset-pick" onClick={() => onValueChange(42)}>
      pick
    </button>
  ),
}));

/** Radix DropdownMenuTrigger opens on pointerdown (button=0) under happy-dom (see TabPanelMenu.test.tsx). */
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

  it("unbound: chip shows the pick placeholder and opens the menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    // The unbound trigger is a single pill button, not a separate label + inline select.
    expect(screen.getByTestId("linked-asset-menu-trigger")).toHaveTextContent("ai.sidebar.linkedAsset.pickPlaceholder");
    openMenu();
    expect(screen.getByTestId("menu-pick-library")).toBeInTheDocument();
  });

  it("binds the asset picked from the library (从资产库选择…)", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    fireEvent.click(screen.getByTestId("menu-pick-library"));
    fireEvent.click(screen.getByTestId("asset-pick"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("lists open terminals and binds the one clicked", () => {
    useTabStore.setState({
      tabs: [
        {
          id: "t7",
          type: "terminal",
          label: "web",
          meta: { type: "terminal", assetId: 7, assetName: "web-07" } as any,
        },
      ],
      activeTabId: "t7",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu();
    fireEvent.click(screen.getByTestId("menu-terminal-7"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(7);
    expect(tab?.linkedAssetName).toBe("web-07");
    expect(tab?.linkedAssetType).toBe("ssh");
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
