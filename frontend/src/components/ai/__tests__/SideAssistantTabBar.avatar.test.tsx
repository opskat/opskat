import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { SideAssistantTabBar } from "../SideAssistantTabBar";
import { useAssetStore } from "@/stores/assetStore";
import type { SidebarAITab, SidebarTabStatus } from "@/stores/aiStore";
import { asset_entity } from "../../../../wailsjs/go/models";

const baseTab: SidebarAITab = {
  id: "s1",
  conversationId: 1,
  title: "prod-web-01",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
};

function renderBar(tabExtra: Partial<SidebarAITab>) {
  return render(
    <SideAssistantTabBar
      tabs={[{ ...baseTab, ...tabExtra }]}
      activeTabId="s1"
      getStatus={(): SidebarTabStatus => "done"}
      collapsed={false}
      onActivate={vi.fn()}
      onClose={vi.fn()}
      onNewChat={vi.fn()}
      onToggleCollapsed={vi.fn()}
    />
  );
}

describe("SideAssistantTabBar avatar", () => {
  beforeEach(() => {
    useAssetStore.setState({
      assets: [new asset_entity.Asset({ ID: 42, Name: "prod-web-01", Type: "ssh", Icon: "server#22c55e" })],
    });
  });

  it("renders the bound asset icon when the tab is bound", () => {
    renderBar({ linkedAssetId: 42, linkedAssetType: "ssh", linkedAssetName: "prod-web-01" });
    expect(screen.getByTestId("session-asset-icon-s1")).toBeInTheDocument();
  });

  it("colors the bound avatar icon with the asset's own color", () => {
    renderBar({ linkedAssetId: 42, linkedAssetType: "ssh", linkedAssetName: "prod-web-01" });
    const svg = screen.getByTestId("session-asset-icon-s1").querySelector("svg");
    expect(svg?.getAttribute("style") ?? "").toContain("color");
  });

  it("falls back to the title letter when unbound", () => {
    renderBar({});
    expect(screen.queryByTestId("session-asset-icon-s1")).toBeNull();
    expect(screen.getByText("P")).toBeInTheDocument(); // getSessionIconLetter("prod-web-01") → "P"
  });
});
