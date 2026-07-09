import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: () => <div data-testid="asset-select" />,
}));

/** Radix DropdownMenuTrigger listens on pointerdown (button=0) to open the menu (see TabPanelMenu.test.tsx). */
function openMenu(trigger: HTMLElement) {
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
}

describe("LinkedAssetControl follow menu", () => {
  beforeEach(() => {
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
          linkedAssetId: 42,
          linkedAssetName: "prod-web-01",
          linkedAssetType: "ssh",
        },
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("toggles follow via the dropdown menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-follow"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.followActiveTerminal).toBe(true);
  });

  it("clears the binding from the menu", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    openMenu(screen.getByTestId("linked-asset-menu-trigger"));
    fireEvent.click(screen.getByTestId("menu-clear"));
    expect(useAIStore.getState().sidebarTabs.find((t) => t.id === "s1")?.linkedAssetId).toBeUndefined();
  });
});
