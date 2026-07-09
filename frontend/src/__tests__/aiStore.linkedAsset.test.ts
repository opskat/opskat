import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { useAIStore } from "../stores/aiStore";
import { useAssetStore } from "../stores/assetStore";
import { useTabStore, type Tab } from "../stores/tabStore";
import { SendAIMessage } from "../../wailsjs/go/ai/AI";
import { buildMentionXml } from "../lib/mentionXml";

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

const queryTab = (
  id: string,
  assetId: number,
  assetName: string,
  assetType: "database" | "redis" | "mongodb" | "kafka" | "k8s" | "etcd"
): Tab => ({
  id,
  type: "query",
  label: assetName,
  meta: {
    type: "query",
    assetId,
    assetName,
    assetIcon: "database",
    assetType,
  },
});

const pageAssetTab = (id: string, assetId: number, label: string): Tab => ({
  id,
  type: "page",
  label,
  meta: { type: "page", pageId: "k8s-cluster", assetId },
});

describe("bindSidebarTab / unbindSidebarTab", () => {
  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({ tabs: [terminalTab("t1", 42, "prod-web-01")], activeTabId: "t1" });
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
        },
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("binds an asset to the tab and persists it", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1" });
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    const persisted = JSON.parse(localStorage.getItem("ai_sidebar_tabs") || "[]");
    expect(persisted[0].linkedAssetId).toBe(42);
  });

  it("clears the binding", () => {
    useAIStore.getState().bindSidebarTab("s1", { workspaceTabId: "t1" });
    useAIStore.getState().unbindSidebarTab("s1");
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});

describe("_sendForConversation includes linked asset in context", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAssetStore.setState({ assets: [] });
    useAIStore.setState({
      configured: true,
      modelName: "gpt-4",
      conversationMessages: { 1: [] },
      conversationStreaming: { 1: { sending: false, pendingQueue: [] } },
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
          linkedAssetId: 99,
          linkedAssetName: "cache",
          linkedAssetType: "redis",
        },
      ],
      activeSidebarTabId: "s1",
    });
  });

  it("prepends the bound asset even when its tab is not open", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined);
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number; assetName: string; type: string }> };
    expect(aiContext.openTabs[0].assetId).toBe(99);
    expect(aiContext.openTabs[0].assetName).toBe("cache");
    expect(aiContext.openTabs[0].type).toBe("redis");
  });

  it("does not duplicate when the bound asset tab is already open", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined);
    useTabStore.setState({
      tabs: [queryTab("q1", 99, "cache", "redis")],
      activeTabId: "q1",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.filter((t) => t.assetId === 99)).toHaveLength(1);
  });

  it("keeps other open tabs after the prepended bound asset", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined);
    useTabStore.setState({
      tabs: [terminalTab("t1", 1, "web"), queryTab("q2", 2, "db", "database")],
      activeTabId: "t1",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.map((t) => t.assetId)).toEqual([99, 1, 2]);
  });

  it("includes open asset page tabs in the sent AI context", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined);
    useAssetStore.setState({ assets: [{ ID: 3, Name: "prod-k8s", Type: "k8s" } as any] });
    useTabStore.setState({
      tabs: [pageAssetTab("k8s-3", 3, "prod-k8s")],
      activeTabId: "k8s-3",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number; assetName: string; type: string }> };
    expect(aiContext.openTabs.map((t) => [t.assetId, t.assetName, t.type])).toContainEqual([3, "prod-k8s", "k8s"]);
  });

  it("does not send an active marker for the prepended bound asset", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined);
    useTabStore.setState({
      tabs: [terminalTab("t1", 1, "web")],
      activeTabId: "t1",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hi");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number; active?: boolean }> };
    expect(aiContext.openTabs[0].assetId).toBe(99);
    expect(aiContext.openTabs.every((t) => !("active" in t))).toBe(true);
  });
});

describe("sendFromSidebarTab auto-binds first mention when unbound", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true,
      modelName: "gpt-4",
      conversationMessages: { 1: [] },
      conversationStreaming: { 1: { sending: false, pendingQueue: [] } },
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
        },
      ],
      activeSidebarTabId: "s1",
    });
    vi.mocked(SendAIMessage).mockResolvedValue(undefined);
  });

  it("sets linkedAssetId from the first mention when that asset has an open tab", async () => {
    useTabStore.setState({ tabs: [terminalTab("t7", 7, "prod-web-01")], activeTabId: "t7" });
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} 帮我看下`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(7);
    expect(tab?.linkedTabId).toBe("t7");
  });

  it("does not auto-bind a mention without an open asset tab", async () => {
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} 帮我看下`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });

  it("does not override an existing binding", async () => {
    useAIStore.setState({
      sidebarTabs: [
        {
          id: "s1",
          conversationId: 1,
          title: "t",
          createdAt: 1,
          uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null },
          linkedAssetId: 99,
          linkedAssetName: "cache",
          linkedAssetType: "redis",
        },
      ],
      activeSidebarTabId: "s1",
    });
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} x`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(99);
  });

  it("includes the auto-bound asset in the sent AI context (first message)", async () => {
    useTabStore.setState({ tabs: [terminalTab("t7", 7, "prod-web-01")], activeTabId: "t7" });
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} 看下`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.some((t) => t.assetId === 7)).toBe(true);
  });
});
