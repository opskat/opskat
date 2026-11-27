import { describe, expect, it } from "vitest";
import { reorderAssetsBatchOptimistically, reorderAssetsOptimistically } from "@/lib/assetTreeReorderOptimistic";

describe("reorderAssetsOptimistically", () => {
  it("moves an asset into the target folder tail immediately", () => {
    const next = reorderAssetsOptimistically(
      [
        { ID: 1, GroupID: 0, SortOrder: 10, Name: "root" },
        { ID: 2, GroupID: 10, SortOrder: 10, Name: "first" },
        { ID: 3, GroupID: 10, SortOrder: 20, Name: "last" },
      ],
      1,
      10,
      0
    );
    expect(next.filter((asset) => asset.GroupID === 10).map((asset) => asset.ID)).toEqual([2, 3, 1]);
    expect(next.find((asset) => asset.ID === 1)?.GroupID).toBe(10);
  });

  it("moves multiple assets preserving caller order before target sibling", () => {
    const next = reorderAssetsBatchOptimistically(
      [
        { ID: 1, GroupID: 0, SortOrder: 10, Name: "r1" },
        { ID: 2, GroupID: 20, SortOrder: 10, Name: "g20-1" },
        { ID: 3, GroupID: 10, SortOrder: 10, Name: "g10-1" },
        { ID: 4, GroupID: 10, SortOrder: 20, Name: "g10-2" },
      ],
      [2, 1],
      10,
      4
    );
    expect(next.filter((asset) => asset.GroupID === 10).map((asset) => asset.ID)).toEqual([3, 2, 1, 4]);
    expect(next.filter((asset) => asset.GroupID === 10).map((asset) => asset.SortOrder)).toEqual([10, 20, 30, 40]);
  });
});
