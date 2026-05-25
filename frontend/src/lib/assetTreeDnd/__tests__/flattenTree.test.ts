import { describe, it, expect } from "vitest";
import { flattenTree } from "../flattenTree";
import { asset_entity, group_entity } from "../../../../wailsjs/go/models";

function g(id: number, parentID = 0): group_entity.Group {
  return new group_entity.Group({ ID: id, ParentID: parentID, Status: 1 });
}
function a(id: number, groupID: number): asset_entity.Asset {
  return new asset_entity.Asset({ ID: id, GroupID: groupID, Status: 1 });
}

describe("flattenTree", () => {
  it("根级 group 按顺序 → header 在前、内部 children 先 sub-group 后 asset", () => {
    const rows = flattenTree({
      groups: [g(1), g(2)],
      assets: [a(10, 1), a(11, 1), a(20, 2)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows).toEqual([
      { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
      { kind: "asset", assetID: 10, groupID: 1, depth: 1 },
      { kind: "asset", assetID: 11, groupID: 1, depth: 1 },
      { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
      { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
    ]);
  });

  it("嵌套 group: 父 group 的 child group 先 push，再 push 父 group 的直接 asset", () => {
    const rows = flattenTree({
      groups: [g(1), g(11, 1)],
      assets: [a(100, 1), a(110, 11)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(
      rows.map((r) => {
        if (r.kind === "asset") return `asset-${r.assetID}`;
        if (r.kind === "group-header") return `group-header-${r.groupID}`;
        return `empty-${r.groupID}`;
      })
    ).toEqual(["group-header-1", "group-header-11", "asset-110", "asset-100"]);
  });

  it("折叠 group → 不展开其子项，但 header 仍然 push", () => {
    const rows = flattenTree({
      groups: [g(1), g(2)],
      assets: [a(10, 1), a(20, 2)],
      collapsedGroupIDs: new Set([1]),
      shouldHideEmpty: false,
    });
    expect(rows).toEqual([
      { kind: "group-header", groupID: 1, depth: 0, collapsed: true },
      { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
      { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
    ]);
  });

  it("空 group 展开 → 出现 empty-placeholder", () => {
    const rows = flattenTree({
      groups: [g(1)],
      assets: [],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows).toEqual([
      { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
      { kind: "empty-placeholder", groupID: 1, depth: 1 },
    ]);
  });

  it("shouldHideEmpty=true → 整个空 group 不出现", () => {
    const rows = flattenTree({
      groups: [g(1), g(2)],
      assets: [a(20, 2)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: true,
    });
    expect(rows.find((r) => r.kind === "group-header" && r.groupID === 1)).toBeUndefined();
    expect(rows.find((r) => r.kind === "group-header" && r.groupID === 2)).toBeDefined();
  });

  it("未分组桶: 有 root assets → 末尾追加 group-header(0) + 其 assets", () => {
    const rows = flattenTree({
      groups: [g(1)],
      assets: [a(10, 1), a(99, 0)],
      collapsedGroupIDs: new Set(),
      shouldHideEmpty: false,
    });
    expect(rows[rows.length - 2]).toEqual({ kind: "group-header", groupID: 0, depth: 0, collapsed: false });
    expect(rows[rows.length - 1]).toEqual({ kind: "asset", assetID: 99, groupID: 0, depth: 1 });
  });
});
