import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { SideAssistantTabBar } from "../SideAssistantTabBar";

const baseTab = { id: "s1", conversationId: 1, title: "prod-web-01", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } };

function renderBar(tabExtra: object) {
  return render(
    <SideAssistantTabBar
      tabs={[{ ...baseTab, ...tabExtra } as any]}
      activeTabId="s1"
      getStatus={() => "done" as any}
      collapsed={false}
      onActivate={vi.fn()}
      onClose={vi.fn()}
      onNewChat={vi.fn()}
      onToggleCollapsed={vi.fn()}
    />
  );
}

describe("SideAssistantTabBar avatar", () => {
  it("renders the asset-type icon when the tab is bound", () => {
    renderBar({ linkedAssetId: 42, linkedAssetType: "ssh", linkedAssetName: "prod-web-01" });
    expect(screen.getByTestId("session-asset-icon-s1")).toBeInTheDocument();
  });

  it("falls back to the title letter when unbound", () => {
    renderBar({});
    expect(screen.queryByTestId("session-asset-icon-s1")).toBeNull();
    expect(screen.getByText("P")).toBeInTheDocument(); // getSessionIconLetter("prod-web-01") → "P"
  });
});
