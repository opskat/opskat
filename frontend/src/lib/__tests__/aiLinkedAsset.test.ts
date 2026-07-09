import { describe, it, expect } from "vitest";
import { linkedAssetFromMention } from "../aiLinkedAsset";

describe("linkedAssetFromMention", () => {
  it("maps mention attrs to linked asset meta", () => {
    expect(linkedAssetFromMention({ assetId: 5, name: "prod-web-01", type: "ssh" })).toEqual({
      assetId: 5, assetName: "prod-web-01", assetType: "ssh",
    });
  });
  it("defaults type to empty string when absent", () => {
    expect(linkedAssetFromMention({ assetId: 5, name: "x" }).assetType).toBe("");
  });
});
