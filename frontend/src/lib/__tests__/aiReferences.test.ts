import { describe, it, expect, beforeEach, vi } from "vitest";
import { buildMentionXml } from "../mentionXml";

// vi.mock factories run during module-graph evaluation, before this file's own
// top-level statements — a plain `const openAssetConnection = vi.fn()` referenced
// directly (not behind a nested closure) hits a TDZ ReferenceError. vi.hoisted
// lifts the fn creation alongside the mock registration to avoid that.
const { activateTab, openAssetConnection } = vi.hoisted(() => ({
  activateTab: vi.fn(),
  openAssetConnection: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("@/stores/tabStore", () => ({
  useTabStore: {
    getState: () => ({ tabs: [{ id: "t1", type: "terminal", label: "web", meta: { assetId: 5 } }], activateTab }),
  },
}));
vi.mock("@/stores/assetStore", () => ({
  useAssetStore: { getState: () => ({ assets: [{ ID: 9, Name: "cache", Type: "redis" }] }) },
}));
vi.mock("../openAsset", () => ({ openAssetConnection }));

import { deriveReferences, jumpToAsset, isAssetTabOpen } from "../aiReferences";

describe("deriveReferences", () => {
  it("collects unique mentioned assets from user messages, excluding the bound one", () => {
    const messages = [
      { role: "user", content: `${buildMentionXml({ assetId: 5, name: "web", type: "ssh" })} a` },
      { role: "assistant", content: "ignored" },
      {
        role: "user",
        content: `${buildMentionXml({ assetId: 9, name: "cache", type: "redis" })} ${buildMentionXml({ assetId: 5, name: "web", type: "ssh" })} b`,
      },
    ];
    const refs = deriveReferences(messages, { excludeAssetId: 5 });
    expect(refs.map((r) => r.assetId)).toEqual([9]);
  });
});

describe("jumpToAsset", () => {
  beforeEach(() => vi.clearAllMocks());
  it("activates an already-open tab (reuse)", async () => {
    expect(isAssetTabOpen(5)).toBe(true);
    await jumpToAsset(5);
    expect(activateTab).toHaveBeenCalledWith("t1");
    expect(openAssetConnection).not.toHaveBeenCalled();
  });
  it("opens a new connection when no tab is open", async () => {
    expect(isAssetTabOpen(9)).toBe(false);
    await jumpToAsset(9);
    expect(openAssetConnection).toHaveBeenCalledTimes(1);
  });
});
