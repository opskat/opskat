import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";
import { useAssetStore } from "../stores/assetStore";
import { useTabStore, type Tab } from "../stores/tabStore";
import type { SidebarAITab } from "../stores/aiStore";

const mkTab = (over: Partial<SidebarAITab> = {}): SidebarAITab => ({
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

const terminalTab = (id: string, assetId: number, assetName: string): Tab => ({
  id,
  type: "terminal",
  label: assetName,
  meta: {
    type: "terminal",
    assetId,
    assetName,
    assetIcon: "server",
    host: "127.0.0.1",
    port: 22,
    username: "root",
  },
});

describe("bindSidebarTab", () => {
  beforeEach(() => {
    localStorage.clear();
    useAssetStore.setState({ assets: [] });
    useTabStore.setState({ tabs: [terminalTab("t1", 5, "web")], activeTabId: "t1" });
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1" })], activeSidebarTabId: "s1" });
  });

  it("binds a workspace tab + derived asset to the conversation", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1" });
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBe("t1");
    expect(tab?.linkedAssetId).toBe(5);
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("does not auto-enable sync on bind", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1" });
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBeFalsy();
  });

  it("1:1 exclusive — binding a tab already bound by another conversation steals it", () => {
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }), mkTab({ id: "s2" })],
    });
    useAIStore.getState().bindSidebarTab("s2", { workspaceTabId: "t1" });
    const s1 = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    const s2 = useAIStore.getState().sidebarTabs.find((x) => x.id === "s2");
    expect(s1?.linkedTabId).toBeNull(); // stolen
    expect(s1?.syncTab).toBe(false); // sync disabled when tab link lost
    expect(s1?.linkedAssetId).toBe(5); // asset context preserved
    expect(s2?.linkedTabId).toBe("t1");
  });

  it("ignores binding to a non-asset or missing workspace tab", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "missing" });
    const tab = useAIStore.getState().sidebarTabs.find((x) => x.id === "s1");
    expect(tab?.linkedTabId).toBeUndefined();
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});

describe("unbindSidebarTab / setSidebarTabSync", () => {
  beforeEach(() => {
    localStorage.clear();
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, linkedAssetType: "ssh", syncTab: true })],
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
    useAIStore.setState({ sidebarTabs: [mkTab({ id: "s1", linkedTabId: null, linkedAssetId: 5 })] });
    useAIStore.getState().setSidebarTabSync("s1", true);
    expect(useAIStore.getState().sidebarTabs.find((x) => x.id === "s1")?.syncTab).toBe(false);
  });
});

describe("bidirectional tab↔conversation sync", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({
      tabs: [terminalTab("t1", 5, "web"), terminalTab("t2", 9, "cache")],
      activeTabId: "t1",
    });
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, linkedAssetType: "ssh", syncTab: true }),
        mkTab({ id: "s2", linkedTabId: "t2", linkedAssetId: 9, linkedAssetType: "ssh", syncTab: true }),
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
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }),
        mkTab({ id: "s2", linkedTabId: "t2", linkedAssetId: 9, syncTab: false }),
      ],
      activeSidebarTabId: "s1",
    });
    useTabStore.setState({ activeTabId: "t2" });
    expect(useAIStore.getState().activeSidebarTabId).toBe("s1"); // unchanged
  });

  it("B: activating a synced conversation whose bound tab is gone leaves the active tab unchanged", () => {
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s3", linkedTabId: "closed", linkedAssetId: 5, syncTab: true })],
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

describe("linkedTabId migrates on tab id replace (reconnect)", () => {
  it("moves linkedTabId when a bound tab's id is replaced", () => {
    localStorage.clear();
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "old-conn", linkedAssetId: 5, syncTab: true })],
      activeSidebarTabId: "s1",
    });
    // aiStore 模块加载时已 registerTabReplaceHook；直接触发一次 replace 语义：
    useTabStore.getState().replaceTabId("old-conn", "new-session");
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("new-session");
  });
});

describe("sync live-tab invariant", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({
      tabs: [terminalTab("t1", 5, "web")],
      activeTabId: "t1",
    });
  });

  it("binding to a missing tab leaves an existing synced binding unchanged", () => {
    useAIStore.setState({
      sidebarTabs: [mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true })],
      activeSidebarTabId: "s1",
    });
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "missing" });
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedTabId).toBe("t1");
    expect(tab?.syncTab).toBe(true);
  });

  it("direction A does not activate a synced conversation whose linkedTabId is null when the active tab clears", () => {
    useAIStore.setState({
      sidebarTabs: [
        mkTab({ id: "s1", linkedTabId: "t1", linkedAssetId: 5, syncTab: true }),
        mkTab({ id: "s2", linkedTabId: null, linkedAssetId: 9, syncTab: true }),
      ],
      activeSidebarTabId: "s1",
    });
    useTabStore.setState({ activeTabId: null }); // all workspace tabs closed
    expect(useAIStore.getState().activeSidebarTabId).toBe("s1"); // s2 must NOT be activated
  });
});
