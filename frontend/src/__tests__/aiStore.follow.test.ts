import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";
import { useTabStore } from "../stores/tabStore";

const mkTab = (over: Partial<{ id: string; conversationId: number; followActiveTerminal: boolean; linkedAssetId: number }>) => ({
  id: over.id ?? "s1",
  conversationId: over.conversationId ?? 1,
  title: "t",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  followActiveTerminal: over.followActiveTerminal,
  linkedAssetId: over.linkedAssetId,
});

describe("setSidebarTabFollow", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({
      tabs: [{ id: "t1", type: "terminal", label: "web", meta: { assetId: 5, assetName: "web" } } as any],
      activeTabId: "t1",
    });
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1" }) as any], activeSidebarTabId: "s1" });
  });

  it("enabling follow binds to the current active terminal immediately", () => {
    useAIStore.getState().setSidebarTabFollow("s1", true);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.followActiveTerminal).toBe(true);
    expect(tab?.linkedAssetId).toBe(5);
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("disabling follow keeps the binding but clears the flag", () => {
    useAIStore.getState().setSidebarTabFollow("s1", true);
    useAIStore.getState().setSidebarTabFollow("s1", false);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.followActiveTerminal).toBe(false);
    expect(tab?.linkedAssetId).toBe(5);
  });
});
