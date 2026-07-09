import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SideAssistantContextBar } from "../SideAssistantContextBar";
import { useAIStore } from "@/stores/aiStore";
import type { SidebarAITab } from "@/stores/aiStore";
import { conversation_entity } from "../../../../wailsjs/go/models";

const sidebarTab: SidebarAITab = {
  id: "s1",
  conversationId: 1,
  title: "会话",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
};

describe("SideAssistantContextBar collapse", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      conversations: [new conversation_entity.Conversation({ ID: 1, Title: "会话" })],
      sidebarTabs: [sidebarTab],
      activeSidebarTabId: "s1",
    });
  });

  it("hides the linked-assets section by default (collapsed)", () => {
    render(<SideAssistantContextBar conversationId={1} sidebarTabId="s1" />);
    expect(screen.queryByTestId("linked-asset-section")).toBeNull();
  });

  it("reveals the section after clicking the disclosure", () => {
    render(<SideAssistantContextBar conversationId={1} sidebarTabId="s1" />);
    fireEvent.click(screen.getByTestId("context-disclosure"));
    expect(screen.getByTestId("linked-asset-section")).toBeInTheDocument();
  });
});
