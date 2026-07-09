import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";

const mkTab = (over: Partial<Record<string, unknown>> = {}) => ({
  id: over.id ?? "s1",
  conversationId: over.conversationId ?? 1,
  title: "t",
  createdAt: 1,
  uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
  linkedTabId: over.linkedTabId,
  linkedAssetId: over.linkedAssetId,
  linkedAssetName: over.linkedAssetName,
  linkedAssetType: over.linkedAssetType,
  syncTab: over.syncTab,
});

describe("bindSidebarTab", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1" }) as any], activeSidebarTabId: "s1" });
  });

  it("binds a workspace tab + derived asset to the conversation", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1", assetId: 5, assetName: "web", assetType: "ssh" });
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBe("t1");
    expect(tab?.linkedAssetId).toBe(5);
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("does not auto-enable sync on bind", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1", assetId: 5, assetName: "web", assetType: "ssh" });
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBeFalsy();
  });

  it("1:1 exclusive — binding a tab already bound by another conversation steals it", () => {
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }) as any,
        mkTab({ id: "s2" }) as any,
      ],
    });
    useAIStore.getState().bindSidebarTab("s2", { workspaceTabId: "t1", assetId: 5, assetName: "web", assetType: "ssh" });
    const s1 = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    const s2 = useAIStore.getState().sidebarTabs.find((x) => x.id === "s2");
    expect(s1?.linkedTabId).toBeNull();        // stolen
    expect(s1?.syncTab).toBe(false);           // sync disabled when tab link lost
    expect(s1?.linkedAssetId).toBe(5);         // asset context preserved
    expect(s2?.linkedTabId).toBe("t1");
  });

  it("library binding (no open tab) sets asset only, leaves linkedTabId null", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: null, assetId: 7, assetName: "db", assetType: "mysql" });
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBeNull();
    expect(tab?.linkedAssetId).toBe(7);
  });
});

describe("unbindSidebarTab / setSidebarTabSync", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, linkedAssetType: "ssh", syncTab: true }) as any],
      activeSidebarTabId: "s1",
    });
  });

  it("unbind clears tab link, asset, and sync", () => {
    useAIStore.getState().unbindSidebarTab("s1");
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBeFalsy();
    expect(tab?.linkedAssetId).toBeUndefined();
    expect(tab?.syncTab).toBe(false);
  });

  it("setSidebarTabSync(true) enables sync when a tab is linked", () => {
    useAIStore.getState().setSidebarTabSync("s1", true);
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBe(true);
  });

  it("setSidebarTabSync(true) is a no-op with no linked tab (asset-only binding)", () => {
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1", linkedTabId: null, linkedAssetId: 5 }) as any] });
    useAIStore.getState().setSidebarTabSync("s1", true);
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBe(false);
  });
});
