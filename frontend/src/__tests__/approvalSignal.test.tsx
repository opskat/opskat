import { describe, it, expect, beforeEach, vi } from "vitest";
import { act, render, renderHook, screen } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";

vi.mock("../i18n", () => ({
  default: { t: (key: string, fallback: string) => fallback || key },
}));
const toastWarning = vi.fn();
const toastDismiss = vi.fn();
vi.mock("sonner", () => ({
  toast: { warning: (...a: unknown[]) => toastWarning(...a), dismiss: (...a: unknown[]) => toastDismiss(...a) },
}));

import { useAIStore, selectWaitingApprovalConversationIds } from "../stores/aiStore";
import { TopBar } from "../components/layout/TopBar";
import { useApprovalNotice } from "../hooks/useApprovalNotice";

const pending = [
  { role: "assistant", content: "", blocks: [{ type: "tool", content: "", status: "pending_confirm" }] },
];
const done = [{ role: "assistant", content: "ok", blocks: [{ type: "text", content: "ok", status: "completed" }] }];
const idle = { sending: false, pendingQueue: [] };

function setConv(messages: Record<number, unknown[]>) {
  const streaming: Record<number, typeof idle> = {};
  for (const k of Object.keys(messages)) streaming[Number(k)] = idle;
  useAIStore.setState({
    conversations: [
      { ID: 7, Title: "Deploy fix" },
      { ID: 8, Title: "Other" },
    ] as never,
    conversationMessages: messages as never,
    conversationStreaming: streaming as never,
    sidebarTabs: [],
  });
}

describe("waiting approval signal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setConv({});
  });

  it("selector lists only conversations with a pending approval", () => {
    setConv({ 7: pending, 8: done });
    expect(selectWaitingApprovalConversationIds(useAIStore.getState())).toEqual([7]);
  });

  function renderTopBar(collapsed = true) {
    return render(
      <TooltipProvider>
        <TopBar
          commandOpen={false}
          onCommandOpenChange={vi.fn()}
          onConnectAsset={vi.fn()}
          assetTreeCollapsed={false}
          onToggleAssetTree={vi.fn()}
          aiPanelCollapsed={collapsed}
          onToggleAIPanel={vi.fn()}
        />
      </TooltipProvider>
    );
  }

  it("top bar AI toggle shows the pending count and clears when resolved", () => {
    setConv({ 7: pending, 8: pending });
    renderTopBar();
    expect(screen.getByTestId("ai-panel-approval-badge").textContent).toBe("2");
    act(() => setConv({ 7: done, 8: done }));
    expect(screen.queryByTestId("ai-panel-approval-badge")).toBeNull();
  });

  it("collapsed panel: warns once with the title and a view action that expands and activates", () => {
    const expand = vi.fn();
    const { rerender } = renderHook(({ collapsed }) => useApprovalNotice(collapsed, expand), {
      initialProps: { collapsed: true },
    });
    expect(toastWarning).not.toHaveBeenCalled();
    act(() => setConv({ 7: pending }));
    expect(toastWarning).toHaveBeenCalledTimes(1);
    const [title, opts] = toastWarning.mock.calls[0];
    expect(title).toContain("Deploy fix");
    rerender({ collapsed: true });
    act(() => useAIStore.setState({ conversationStreaming: { 7: { ...idle } } as never }));
    expect(toastWarning).toHaveBeenCalledTimes(1);

    act(() => opts.action.onClick());
    expect(expand).toHaveBeenCalled();
    const st = useAIStore.getState();
    expect(st.sidebarTabs.find((t) => t.id === st.activeSidebarTabId)?.conversationId).toBe(7);

    act(() => setConv({ 7: done }));
    expect(toastDismiss).toHaveBeenCalledWith(opts.id);
  });

  it("expanded panel: no toast", () => {
    renderHook(() => useApprovalNotice(false, vi.fn()));
    act(() => setConv({ 7: pending }));
    expect(toastWarning).not.toHaveBeenCalled();
  });
});
