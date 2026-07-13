import { describe, it, expect } from "vitest";
import { tabToAssetRef } from "../tabAsset";
import { asset_entity } from "../../../wailsjs/go/models";
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

const k8sPageTab: Tab = {
  id: "k8s-3",
  type: "page",
  label: "prod-k8s",
  meta: { type: "page", pageId: "k8s-cluster", assetId: 3 },
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
  it("maps an asset page tab using the matching asset", () => {
    expect(tabToAssetRef(k8sPageTab, [new asset_entity.Asset({ ID: 3, Name: "prod-k8s", Type: "k8s" })])).toEqual({
      assetId: 3,
      assetName: "prod-k8s",
      assetType: "k8s",
    });
  });
  it("returns null for ai/non-asset page tabs", () => {
    expect(tabToAssetRef(aiTab)).toBeNull();
    expect(tabToAssetRef(pageTab)).toBeNull();
  });
});
