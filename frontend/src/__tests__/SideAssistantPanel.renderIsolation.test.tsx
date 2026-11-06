import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SideAssistantPanel } from "@/components/ai/SideAssistantPanel";
import { useAIStore } from "@/stores/aiStore";
import { ListConversations } from "../../wailsjs/go/ai/AI";
import { profiled } from "./renderCount";

// Store-subscribing children are stubbed so a Profiler commit means SideAssistantPanel itself re-rendered.
vi.mock("@/components/ai/AIChatContent", () => ({ AIChatContent: () => <div data-testid="chat" /> }));
vi.mock("@/components/ai/SideAssistantHeader", () => ({ SideAssistantHeader: () => <div /> }));
vi.mock("@/components/ai/SideAssistantContextBar", () => ({ SideAssistantContextBar: () => <div /> }));
vi.mock("@/components/ai/SideAssistantHistoryDropdown", () => ({ SideAssistantHistoryDropdown: () => <div /> }));

function sidebarTab(id: string, conversationId: number) {
  return {
    id,
    conversationId,
    title: id,
    createdAt: 1,
    uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  };
}

describe("SideAssistantPanel render isolation", () => {
  beforeEach(() => {
    vi.mocked(ListConversations).mockImplementation(async () => useAIStore.getState().conversations as never);
    localStorage.clear();
    useAIStore.setState({
      configured: true,
      conversations: [
        { ID: 1, Title: "sb1" },
        { ID: 2, Title: "other" },
      ] as never,
      conversationMessages: { 1: [], 2: [] },
      conversationStreaming: {},
      sidebarTabs: [sidebarTab("sb1", 1)],
      activeSidebarTabId: "sb1",
      tabStates: {},
    });
  });

  it("does not re-render when a conversation hosted outside the sidebar streams", async () => {
    const { tree, counter } = profiled(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);
    await act(async () => {
      render(tree);
    });
    await screen.findByTestId("chat");
    const before = counter.commits;

    await act(async () => {
      useAIStore.setState((s) => ({
        conversationMessages: {
          ...s.conversationMessages,
          2: [{ role: "assistant", content: "partial", streaming: true, blocks: [] } as never],
        },
        conversationStreaming: { ...s.conversationStreaming, 2: { sending: true, pendingQueue: [] } },
      }));
    });

    expect(counter.commits).toBe(before);
  });
});
