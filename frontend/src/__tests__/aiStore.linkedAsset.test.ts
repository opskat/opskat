import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { sanitizeSidebarTab, useAIStore } from "../stores/aiStore";

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
