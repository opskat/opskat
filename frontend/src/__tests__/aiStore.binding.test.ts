import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";
import { useTabStore } from "../stores/tabStore";

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

describe("bidirectional tab↔conversation sync", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({
      tabs: [
        { id: "t1", type: "terminal", label: "web", meta: { assetId: 5, assetName: "web" } } as any,
        { id: "t2", type: "terminal", label: "cache", meta: { assetId: 9, assetName: "cache" } } as any,
      ],
      activeTabId: "t1",
    });
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, linkedAssetType: "ssh", syncTab: true }) as any,
        mkTab({ id: "s2", linkedTabId: "t2", linkedAssetId: 9, linkedAssetType: "ssh", syncTab: true }) as any,
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("A: switching workspace tab activates the synced conversation bound to it", () => {
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s2");
  });

  it("B: activating a synced conversation activates its bound workspace tab", () => {
    useAIStore.getState().activateSidebarTab("s2");
    expect(useTabStore.getState().activeTabId).toBe("t2");
  });

  it("does not activate a conversation whose sync is off", () => {
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }) as any,
        mkTab({ id: "s2", linkedTabId: "t2", linkedAssetId: 9, syncTab: false }) as any,
      ],
      activeSidebarTabId: "s1",
    });
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s1"); // unchanged
  });

  it("B: activating a synced conversation whose bound tab is gone leaves the active tab unchanged", () => {
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s3", linkedTabId: "closed", linkedAssetId: 5, syncTab: true }) as any],
      activeSidebarTabId: "s1",
    });
    useTabStore.setState({ activeTabId: "t1" });
    useAIStore.getState().activateSidebarTab("s3");
    expect(useTabStore.getState().activeTabId).toBe("t1"); // unchanged
  });

  it("does not infinite-loop between the two directions", () => {
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s2");
    expect(useTabStore.getState().activeTabId).toBe("t2");
  });
});
