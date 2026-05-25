import { describe, it, expect } from "vitest";
import { insertionToReorderArgs } from "../reorderArgs";
import { asset_entity, group_entity } from "../../../../wailsjs/go/models";

function g(id: number, parentID = 0): group_entity.Group {
  return new group_entity.Group({ ID: id, ParentID: parentID, Status: 1 });
}
function a(id: number, groupID: number, sortOrder: number): asset_entity.Asset {
  return new asset_entity.Asset({ ID: id, GroupID: groupID, SortOrder: sortOrder, Status: 1 });
}

describe("insertionToReorderArgs - asset", () => {
  const groups = [g(1), g(2), g(11, 1)];
  const assets = [a(10, 1, 10), a(11, 1, 20), a(20, 2, 10), a(21, 2, 20)];

  it("before-asset → asset to that group, beforeID = the asset", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-asset", assetID: 11, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 11 });
  });

  it("after-asset 中间 → beforeID = next sibling", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 11 });
  });

  it("after-asset 末尾 → beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 11, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 0 });
  });

  it("after-asset 末尾时 next sibling 排除 active 自己", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "asset", id: 11 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 11, targetGroupID: 1, beforeID: 0 });
  });

  it("before-group(G) + asset → 移到 G.ParentID 末尾", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-group", groupID: 11, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 1, beforeID: 0 });
  });

  it("into-group-first(G) + asset, G 已有 assets → beforeID = G 首位 asset", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 2, depth: 0 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 2, beforeID: 20 });
  });

  it("into-group-first(G) + asset, G 没有 asset → beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 11, depth: 1 },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 11, beforeID: 0 });
  });

  it("root-end + asset → targetGroupID = 0, beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "root-end" },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "asset", id: 99, targetGroupID: 0, beforeID: 0 });
  });
});

describe("insertionToReorderArgs - group", () => {
  const groups = [g(1), g(2), g(11, 1), g(12, 1)];
  const assets: asset_entity.Asset[] = [];

  it("before-group(G) + group → 同父级排到 G 之前", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-group", groupID: 2, depth: 0 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 0, beforeID: 2 });
  });

  it("into-group-first(G) + group, G 有子 group → beforeID = G 首位子 group", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 1, depth: 0 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 1, beforeID: 11 });
  });

  it("into-group-first(G) + group, G 无子 group → beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "into-group-first", groupID: 2, depth: 0 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 2, beforeID: 0 });
  });

  it("root-end + group → targetParentID = 0, beforeID = 0", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "root-end" },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toEqual({ kind: "group", id: 99, targetParentID: 0, beforeID: 0 });
  });

  it("before-asset/after-asset + group → null（group 不能落在 asset 之间）", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "before-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toBeNull();
    expect(
      insertionToReorderArgs({
        point: { kind: "after-asset", assetID: 10, groupID: 1, depth: 1 },
        active: { kind: "group", id: 99 },
        groups,
        assets,
      })
    ).toBeNull();
  });

  it("invalid → null", () => {
    expect(
      insertionToReorderArgs({
        point: { kind: "invalid" },
        active: { kind: "asset", id: 99 },
        groups,
        assets,
      })
    ).toBeNull();
  });
});
