import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LinkedAssetControl } from "../LinkedAssetControl";
import { useAIStore } from "@/stores/aiStore";
import { useAssetStore } from "@/stores/assetStore";

vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: ({ onValueChange }: { onValueChange: (id: number) => void }) => (
    <button data-testid="asset-pick" onClick={() => onValueChange(42)}>pick</button>
  ),
}));

describe("LinkedAssetControl", () => {
  beforeEach(() => {
    useAssetStore.setState({ assets: [{ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server" } as any] });
    useAIStore.setState({
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
      activeSidebarTabId: "s1",
    });
  });

  it("binds the picked asset via setSidebarTabAsset", () => {
    render(<LinkedAssetControl sidebarTabId="s1" />);
    fireEvent.click(screen.getByTestId("asset-pick"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("shows the bound chip and clears on clear", () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 42, assetName: "prod-web-01", assetType: "ssh" });
    render(<LinkedAssetControl sidebarTabId="s1" />);
    expect(screen.getByText("prod-web-01")).toBeInTheDocument();
    // Radix DropdownMenuTrigger listens on pointerdown (button=0) to open the menu (see TabPanelMenu.test.tsx).
    fireEvent.pointerDown(screen.getByTestId("linked-asset-menu-trigger"), { button: 0, ctrlKey: false });
    fireEvent.click(screen.getByTestId("menu-clear"));
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
