import { describe, it, expect } from "vitest";
import { tabToAssetRef } from "../tabAsset";

describe("tabToAssetRef", () => {
  it("maps a terminal tab to an ssh asset ref", () => {
    expect(tabToAssetRef({ id: "t", type: "terminal", label: "web", meta: { assetId: 1, assetName: "web" } } as any)).toEqual({
      assetId: 1, assetName: "web", assetType: "ssh",
    });
  });
  it("maps a query tab using its meta.assetType", () => {
    expect(tabToAssetRef({ id: "q", type: "query", label: "db", meta: { assetId: 2, assetName: "db", assetType: "redis" } } as any)).toEqual({
      assetId: 2, assetName: "db", assetType: "redis",
    });
  });
  it("returns null for ai/page tabs", () => {
    expect(tabToAssetRef({ id: "a", type: "ai", label: "AI", meta: {} } as any)).toBeNull();
    expect(tabToAssetRef({ id: "p", type: "page", label: "P", meta: { type: "page" } } as any)).toBeNull();
  });
});
