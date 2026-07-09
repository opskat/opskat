import { describe, it, expect } from "vitest";
import { tabToAssetRef } from "../tabAsset";
import type { Tab } from "@/stores/tabStore";

const terminalTab: Tab = {
  id: "t",
  type: "terminal",
  label: "web",
  meta: {
    type: "terminal",
    assetId: 1,
    assetName: "web",
    assetIcon: "server",
    host: "127.0.0.1",
    port: 22,
    username: "root",
  },
};

const queryTab: Tab = {
  id: "q",
  type: "query",
  label: "db",
  meta: {
    type: "query",
    assetId: 2,
    assetName: "db",
    assetIcon: "database",
    assetType: "redis",
  },
};

const aiTab: Tab = {
  id: "a",
  type: "ai",
  label: "AI",
  meta: { type: "ai", conversationId: null, title: "AI" },
};

const pageTab: Tab = {
  id: "p",
  type: "page",
  label: "P",
  meta: { type: "page", pageId: "settings" },
};

describe("tabToAssetRef", () => {
  it("maps a terminal tab to an ssh asset ref", () => {
    expect(tabToAssetRef(terminalTab)).toEqual({
      assetId: 1,
      assetName: "web",
      assetType: "ssh",
    });
  });
  it("maps a query tab using its meta.assetType", () => {
    expect(tabToAssetRef(queryTab)).toEqual({
      assetId: 2,
      assetName: "db",
      assetType: "redis",
    });
  });
  it("returns null for ai/page tabs", () => {
    expect(tabToAssetRef(aiTab)).toBeNull();
    expect(tabToAssetRef(pageTab)).toBeNull();
  });
});
