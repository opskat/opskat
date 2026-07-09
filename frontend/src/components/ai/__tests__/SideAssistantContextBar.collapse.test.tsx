import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SideAssistantContextBar } from "../SideAssistantContextBar";
import { useAIStore } from "@/stores/aiStore";

describe("SideAssistantContextBar collapse", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "会话" } as any],
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "会话", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
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
