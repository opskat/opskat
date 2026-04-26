/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";
import { useAIStore } from "../stores/aiStore";
import { useTabStore } from "../stores/tabStore";
import { SideAssistantPanel } from "../components/ai/SideAssistantPanel";
import {
  CreateConversation,
  ListConversations,
  LoadConversationMessages,
  DeleteConversation,
} from "../../wailsjs/go/app/App";

// Note: setup.ts mocks react-i18next so `t(key)` returns the raw key.
// So button titles become the i18n keys themselves (e.g. "ai.sidebar.newChat").

const defaultAIActions = {
  renameConversation: useAIStore.getState().renameConversation,
};

describe("SideAssistantPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true,
      conversations: [],
      conversationMessages: {},
      conversationStreaming: {},
      renameConversation: defaultAIActions.renameConversation,
      sidebarConversationId: null,
      sidebarUIState: { inputDraft: "", scrollTop: 0 },
      tabStates: {},
    });
    // Prevent fetchConversations (called on mount) from clobbering our seeded conversations.
    vi.mocked(ListConversations).mockImplementation(async () => {
      return useAIStore.getState().conversations as any;
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("collapsed state collapses outer width to 0 (panel stays in DOM for width animation)", () => {
    const { container } = render(<SideAssistantPanel collapsed={true} onToggle={() => {}} />);
    // Outer wrapper animates via width; collapsed means width: 0.
    const outer = container.firstChild as HTMLElement;
    expect(outer).toBeTruthy();
    expect(outer.style.width).toBe("0px");
  });

  it("expanded with no conversation shows empty guide", () => {
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);
    // With the mocked t() returning keys, the empty guide renders the raw key "ai.sidebar.emptyGuide".
    expect(screen.getByText("ai.sidebar.emptyGuide")).toBeInTheDocument();
  });

  it("clicking + new chat creates a conversation and binds sidebar", async () => {
    vi.mocked(CreateConversation).mockResolvedValue({ ID: 123, Title: "", Updatetime: 0 } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    const newBtn = screen.getByTitle("ai.sidebar.newChat");
    fireEvent.click(newBtn);

    await waitFor(() => {
      expect(useAIStore.getState().sidebarConversationId).toBe(123);
    });
  });

  it("history button opens dropdown and selecting binds sidebar", async () => {
    useAIStore.setState({
      conversations: [
        { ID: 1, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any,
        { ID: 2, Title: "Conv B", Updatetime: Math.floor(Date.now() / 1000) } as any,
      ],
    });
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    expect(await screen.findByText("Conv A")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Conv A"));
    expect(useAIStore.getState().sidebarConversationId).toBe(1);
  });

  it("clicking outside the history popup closes it (within panel)", async () => {
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
      sidebarConversationId: null,
    });
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    expect(await screen.findByText("Conv A")).toBeInTheDocument();

    // Click somewhere inside the panel but outside the dropdown popup.
    fireEvent.mouseDown(screen.getByText("ai.sidebar.emptyGuide"));

    await waitFor(() => {
      expect(screen.queryByText("Conv A")).not.toBeInTheDocument();
    });
  });

  it("clicking outside the history popup closes it (outside panel)", async () => {
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
    });
    render(
      <div>
        <button data-testid="outside">outside</button>
        <SideAssistantPanel collapsed={false} onToggle={() => {}} />
      </div>
    );

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    expect(await screen.findByText("Conv A")).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByTestId("outside"));

    await waitFor(() => {
      expect(screen.queryByText("Conv A")).not.toBeInTheDocument();
    });
  });

  it("clicking inside the history popup keeps it open", async () => {
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
    });
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    const item = await screen.findByText("Conv A");

    // mousedown inside the popup must not close it (the search input/list area).
    fireEvent.mouseDown(item);
    expect(screen.getByText("Conv A")).toBeInTheDocument();
  });

  it("confirming delete in the history popup triggers DeleteConversation (dropdown close must not preempt portal click)", async () => {
    vi.mocked(DeleteConversation).mockResolvedValue(undefined);
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
    });
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    await screen.findByText("Conv A");
    fireEvent.click(screen.getByTitle("action.delete"));

    // Delete confirmation is a Popover portaled to body. mousedown on the Confirm
    // button must not cause the panel's click-outside handler to unmount the dropdown
    // (and with it, the popover) before click fires.
    const confirmBtn = await screen.findByText("action.delete");
    fireEvent.click(confirmBtn);

    await waitFor(() => {
      expect(DeleteConversation).toHaveBeenCalledWith(1);
    });
  });

  it("clicking promote button promotes sidebar conversation and clears sidebar binding", async () => {
    vi.mocked(LoadConversationMessages).mockResolvedValue([] as any);
    useAIStore.setState({
      sidebarConversationId: 5,
      conversations: [{ ID: 5, Title: "Conv", Updatetime: 0 } as any],
      conversationMessages: { 5: [] },
      conversationStreaming: { 5: { sending: false, pendingQueue: [] } },
    });
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.promoteToTab"));

    await waitFor(() => {
      expect(useAIStore.getState().sidebarConversationId).toBeNull();
    });
  });

  it("current conversation can be renamed from the context bar", async () => {
    const renameConversation = vi.fn().mockResolvedValue(true);
    useAIStore.setState({
      conversations: [{ ID: 7, Title: "旧标题", Updatetime: Math.floor(Date.now() / 1000) } as any],
      conversationMessages: { 7: [] },
      conversationStreaming: { 7: { sending: false, pendingQueue: [] } },
      sidebarConversationId: 7,
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    fireEvent.change(screen.getByPlaceholderText("ai.renameConversationPlaceholder"), {
      target: { value: "新标题" },
    });
    fireEvent.click(screen.getByTitle("action.save"));

    await waitFor(() => {
      expect(renameConversation).toHaveBeenCalledWith(7, "新标题");
    });
  });

  it("context-bar rename ignores Enter while IME composition is active", () => {
    const renameConversation = vi.fn().mockResolvedValue(true);
    useAIStore.setState({
      conversations: [{ ID: 71, Title: "旧标题", Updatetime: Math.floor(Date.now() / 1000) } as any],
      conversationMessages: { 71: [] },
      conversationStreaming: { 71: { sending: false, pendingQueue: [] } },
      sidebarConversationId: 71,
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    const input = screen.getByPlaceholderText("ai.renameConversationPlaceholder");
    fireEvent.change(input, { target: { value: "输入中" } });
    fireEvent.keyDown(input, { key: "Enter", isComposing: true });

    expect(renameConversation).not.toHaveBeenCalled();
  });

  it("context-bar rename stays disabled until the conversation metadata is loaded", () => {
    useAIStore.setState({
      conversationMessages: { 11: [] },
      conversationStreaming: { 11: { sending: false, pendingQueue: [] } },
      sidebarConversationId: 11,
      conversations: [],
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    expect(screen.getByTitle("ai.renameConversation")).toBeDisabled();
  });

  it("context-bar rename keeps edit mode open when the save fails", async () => {
    const renameConversation = vi.fn().mockResolvedValue(false);
    useAIStore.setState({
      conversations: [{ ID: 8, Title: "旧标题", Updatetime: Math.floor(Date.now() / 1000) } as any],
      conversationMessages: { 8: [] },
      conversationStreaming: { 8: { sending: false, pendingQueue: [] } },
      sidebarConversationId: 8,
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    fireEvent.change(screen.getByPlaceholderText("ai.renameConversationPlaceholder"), {
      target: { value: "失败标题" },
    });
    fireEvent.click(screen.getByTitle("action.save"));

    await waitFor(() => {
      expect(renameConversation).toHaveBeenCalledWith(8, "失败标题");
    });
    expect(screen.getByPlaceholderText("ai.renameConversationPlaceholder")).toBeInTheDocument();
  });

  it("a stale context-bar rename completion does not close the next conversation editor", async () => {
    let resolveFirstRename: ((value: boolean) => void) | undefined;
    const renameConversation = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<boolean>((resolve) => {
            resolveFirstRename = resolve;
          })
      )
      .mockResolvedValue(false);

    useAIStore.setState({
      conversations: [
        { ID: 21, Title: "会话 A", Updatetime: Math.floor(Date.now() / 1000) } as any,
        { ID: 22, Title: "会话 B", Updatetime: Math.floor(Date.now() / 1000) } as any,
      ],
      conversationMessages: {
        21: [],
        22: [],
      },
      conversationStreaming: {
        21: { sending: false, pendingQueue: [] },
        22: { sending: false, pendingQueue: [] },
      },
      sidebarConversationId: 21,
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    fireEvent.change(screen.getByPlaceholderText("ai.renameConversationPlaceholder"), {
      target: { value: "会话 A 新标题" },
    });
    fireEvent.click(screen.getByTitle("action.save"));

    useAIStore.setState({ sidebarConversationId: 22 } as any);
    await waitFor(() => {
      expect(screen.getByTitle("ai.renameConversation")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    expect(screen.getByPlaceholderText("ai.renameConversationPlaceholder")).toBeInTheDocument();

    resolveFirstRename?.(true);

    await waitFor(() => {
      expect(screen.getByPlaceholderText("ai.renameConversationPlaceholder")).toBeInTheDocument();
    });
  });

  it("history rename edits the conversation without rebinding the sidebar", async () => {
    const renameConversation = vi.fn().mockResolvedValue(true);
    useAIStore.setState({
      conversations: [{ ID: 1, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    await screen.findByText("Conv A");
    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    fireEvent.change(screen.getByPlaceholderText("ai.renameConversationPlaceholder"), {
      target: { value: "Conv Renamed" },
    });
    fireEvent.click(screen.getByTitle("action.save"));

    await waitFor(() => {
      expect(renameConversation).toHaveBeenCalledWith(1, "Conv Renamed");
    });
    expect(useAIStore.getState().sidebarConversationId).toBeNull();
  });

  it("history rename ignores Enter while IME composition is active", async () => {
    const renameConversation = vi.fn().mockResolvedValue(true);
    useAIStore.setState({
      conversations: [{ ID: 72, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    await screen.findByText("Conv A");
    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    const input = screen.getByPlaceholderText("ai.renameConversationPlaceholder");
    fireEvent.change(input, { target: { value: "输入中" } });
    fireEvent.keyDown(input, { key: "Enter", isComposing: true });

    expect(renameConversation).not.toHaveBeenCalled();
  });

  it("history rename ignores repeated saves while a rename is in flight", async () => {
    let resolveRename: ((value: boolean) => void) | undefined;
    const renameConversation = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          resolveRename = resolve;
        })
    );
    useAIStore.setState({
      conversations: [{ ID: 12, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any],
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    await screen.findByText("Conv A");
    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    fireEvent.change(screen.getByPlaceholderText("ai.renameConversationPlaceholder"), {
      target: { value: "Conv Once" },
    });
    fireEvent.click(screen.getByTitle("action.save"));
    fireEvent.click(screen.getByTitle("action.save"));

    expect(renameConversation).toHaveBeenCalledTimes(1);

    resolveRename?.(true);
    await waitFor(() => {
      expect(screen.queryByPlaceholderText("ai.renameConversationPlaceholder")).not.toBeInTheDocument();
    });
  });

  it("history rename does not switch to another row while the current save is in flight", async () => {
    let resolveRename: ((value: boolean) => void) | undefined;
    const renameConversation = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          resolveRename = resolve;
        })
    );
    useAIStore.setState({
      conversations: [
        { ID: 31, Title: "Conv A", Updatetime: Math.floor(Date.now() / 1000) } as any,
        { ID: 32, Title: "Conv B", Updatetime: Math.floor(Date.now() / 1000) } as any,
      ],
      renameConversation,
    } as any);
    render(<SideAssistantPanel collapsed={false} onToggle={() => {}} />);

    fireEvent.click(screen.getByTitle("ai.sidebar.history"));
    await screen.findByText("Conv A");
    fireEvent.click(screen.getAllByTitle("ai.renameConversation")[0]);
    fireEvent.change(screen.getByPlaceholderText("ai.renameConversationPlaceholder"), {
      target: { value: "Conv A Renamed" },
    });
    fireEvent.click(screen.getByTitle("action.save"));

    fireEvent.click(screen.getByTitle("ai.renameConversation"));
    expect(screen.getByDisplayValue("Conv A Renamed")).toBeInTheDocument();

    resolveRename?.(true);
    await waitFor(() => {
      expect(screen.queryByPlaceholderText("ai.renameConversationPlaceholder")).not.toBeInTheDocument();
    });
  });
});
