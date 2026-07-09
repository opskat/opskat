import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { sanitizeSidebarTab, useAIStore } from "../stores/aiStore";
import { useTabStore } from "../stores/tabStore";
import { SendAIMessage } from "../../wailsjs/go/ai/AI";
import { buildMentionXml } from "../lib/mentionXml";

describe("sanitizeSidebarTab linked asset", () => {
  it("round-trips valid linked asset fields", () => {
    const tab = sanitizeSidebarTab({
      id: "sidebar-1",
      conversationId: 7,
      title: "t",
      createdAt: 1,
      linkedAssetId: 42,
      linkedAssetName: "prod-web-01",
      linkedAssetType: "ssh",
    });
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("drops non-number linkedAssetId to undefined", () => {
    const tab = sanitizeSidebarTab({ id: "s2", linkedAssetId: "nope" });
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});

describe("setSidebarTabAsset / clearSidebarTabAsset", () => {
  beforeEach(() => {
    localStorage.clear();
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
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 42, assetName: "prod-web-01", assetType: "ssh" });
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    const persisted = JSON.parse(localStorage.getItem("ai_sidebar_tabs") || "[]");
    expect(persisted[0].linkedAssetId).toBe(42);
  });

  it("clears the binding", () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 42, assetName: "p", assetType: "ssh" });
    useAIStore.getState().clearSidebarTabAsset("s1");
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});

describe("_sendForConversation includes linked asset in context", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true,
      modelName: "gpt-4",
      conversationMessages: { 1: [] },
      conversationStreaming: { 1: { sending: false, pendingQueue: [] } },
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null }, linkedAssetId: 99, linkedAssetName: "cache", linkedAssetType: "redis" }],
      activeSidebarTabId: "s1",
    });
  });

  it("prepends the bound asset even when its tab is not open", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number; assetName: string; type: string }> };
    expect(aiContext.openTabs[0].assetId).toBe(99);
    expect(aiContext.openTabs[0].assetName).toBe("cache");
    expect(aiContext.openTabs[0].type).toBe("redis");
  });

  it("does not duplicate when the bound asset tab is already open", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
    useTabStore.setState({
      tabs: [{ id: "q1", type: "query", label: "cache", meta: { assetId: 99, assetName: "cache", assetType: "redis" } } as any],
      activeTabId: "q1",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.filter((t) => t.assetId === 99)).toHaveLength(1);
  });

  it("keeps other open tabs after the prepended bound asset", async () => {
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
    useTabStore.setState({
      tabs: [
        { id: "t1", type: "terminal", label: "web", meta: { assetId: 1, assetName: "web", assetType: "ssh" } } as any,
        { id: "q2", type: "query", label: "db", meta: { assetId: 2, assetName: "db", assetType: "mysql" } } as any,
      ],
      activeTabId: "t1",
    });
    await useAIStore.getState().sendFromSidebarTab("s1", "hello");
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.map((t) => t.assetId)).toEqual([99, 1, 2]);
  });
});

describe("sendFromSidebarTab auto-binds first mention when unbound", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAIStore.setState({
      configured: true, modelName: "gpt-4",
      conversationMessages: { 1: [] },
      conversationStreaming: { 1: { sending: false, pendingQueue: [] } },
      sidebarTabs: [{ id: "s1", conversationId: 1, title: "t", createdAt: 1, uiState: { inputDraft: { content: "" }, scrollTop: 0, editTarget: null } }],
      activeSidebarTabId: "s1",
    });
    vi.mocked(SendAIMessage).mockResolvedValue(undefined as any);
  });

  it("sets linkedAssetId from the first mention", async () => {
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} 帮我看下`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(7);
  });

  it("does not override an existing binding", async () => {
    useAIStore.getState().setSidebarTabAsset("s1", { assetId: 99, assetName: "cache", assetType: "redis" });
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} x`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const tab = useAIStore.getState().sidebarTabs.find((t) => t.id === "s1");
    expect(tab?.linkedAssetId).toBe(99);
  });

  it("includes the auto-bound asset in the sent AI context (first message)", async () => {
    const content = `${buildMentionXml({ assetId: 7, name: "prod-web-01", type: "ssh" })} 看下`;
    await useAIStore.getState().sendFromSidebarTab("s1", content);
    const call = vi.mocked(SendAIMessage).mock.calls.at(-1);
    const aiContext = call?.[2] as { openTabs: Array<{ assetId: number }> };
    expect(aiContext.openTabs.some((t) => t.assetId === 7)).toBe(true);
  });
});
