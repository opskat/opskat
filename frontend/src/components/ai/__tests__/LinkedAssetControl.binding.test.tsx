import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";

/** Radix DropdownMenuTrigger opens on pointerdown(button=0). */
function openMenu(trigger: HTMLElement) {
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
}

const boundTab = {
  id: "s1",
  conversationId: 1,
  title: "t",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  linkedTabId: "t1",
  linkedAssetId: 42,
  linkedAssetName: "prod-web-01",
  linkedAssetType: "ssh",
};

describe("LinkedAssetControl binding + sync menu", () => {
  beforeEach(() => {
    useTabStore.setState({
      tabs: [
        { id: "t1", type: "terminal", label: "prod-web-01", meta: { assetId: 42, assetName: "prod-web-01" } } as any,
      ],
      activeTabId: "t1",
    });
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({ sidebarTabs: [boundTab as any], activeSidebarTabId: "s1" });
  });

  it("binds a workspace tab from the open-tabs list", () => {
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
        } as any,
      ],
      activeSidebarTabId: "s1",
    });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-tab-t1"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("t1");
    expect(tab?.linkedAssetId).toBe(42);
  });

  it("toggles sync via the dropdown when a live tab is bound", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-sync"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.syncTab).toBe(true);
    expect(screen.getByTestId("linked-asset-menu-trigger")).toHaveAttribute("title", "ai.sidebar.syncing");
  });

  it("clears the binding from the menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-clear"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBeUndefined();
  });

  it("disables sync when the bound tab is no longer open (asset context kept)", () => {
    useTabStore.setState({ tabs: [], activeTabId: null }); // bound tab closed
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-sync"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.syncTab).toBeFalsy();
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBe(42);
  });
});
