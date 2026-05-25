import { describe, it, expect } from "vitest";
import { computeInsertionPoint, rowKey } from "../insertionPoint";
import type { Row } from "../flattenTree";
import { group_entity } from "../../../../wailsjs/go/models";

function g(id: number, parentID = 0): group_entity.Group {
  return new group_entity.Group({ ID: id, ParentID: parentID, Status: 1 });
}

function makeRects(rows: Row[], rowHeight = 24): Map<string, { top: number; bottom: number; height: number }> {
  const rects = new Map<string, { top: number; bottom: number; height: number }>();
  rows.forEach((row, idx) => {
    rects.set(rowKey(row), { top: idx * rowHeight, bottom: (idx + 1) * rowHeight, height: rowHeight });
  });
  return rects;
}

describe("computeInsertionPoint - asset active", () => {
  const groups = [g(1), g(2)];
  const rows: Row[] = [
    { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
    { kind: "asset", assetID: 10, groupID: 1, depth: 1 },
    { kind: "asset", assetID: 11, groupID: 1, depth: 1 },
    { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
    { kind: "asset", assetID: 20, groupID: 2, depth: 1 },
  ];
  const rects = makeRects(rows);

  it("asset 上半区 → before-asset", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 5, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "before-asset", assetID: 10, groupID: 1, depth: 1 });
  });

  it("asset 下半区（非末尾）→ after-asset", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 18, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "after-asset", assetID: 10, groupID: 1, depth: 1 });
  });

  it("asset 下半区（组末尾）→ after-asset（reorderArgs 阶段映射成末尾）", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 48 + 18, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "after-asset", assetID: 11, groupID: 1, depth: 1 });
  });

  it("asset 拖到 group header 上半区 → into-group-first（避免误落未分组）", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 72 + 5, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "into-group-first", groupID: 2, depth: 0 });
  });

  it("asset 拖到 group header 下半区 → into-group-first", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 72 + 18, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "into-group-first", groupID: 2, depth: 0 });
  });

  it("pointer 超出树底部（asset active）→ invalid（避免无意中落入未分组）", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 9999, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "invalid" });
  });

  it("pointer 超出树底部（group active）→ root-end", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 9999, active: { kind: "group", id: 99 }, groups })
    ).toEqual({ kind: "root-end" });
  });

  it("拖到自己的当前 row → invalid", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 5, active: { kind: "asset", id: 10 }, groups })
    ).toEqual({ kind: "invalid" });
  });
});

describe("computeInsertionPoint - group active", () => {
  const groups = [g(1), g(11, 1), g(2)];
  const rows: Row[] = [
    { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
    { kind: "group-header", groupID: 11, depth: 1, collapsed: true },
    { kind: "group-header", groupID: 2, depth: 0, collapsed: false },
  ];
  const rects = makeRects(rows);

  it("group 拖到 asset row → invalid", () => {
    const rowsWithAsset: Row[] = [...rows, { kind: "asset", assetID: 20, groupID: 2, depth: 1 }];
    const r = makeRects(rowsWithAsset);
    expect(
      computeInsertionPoint({
        rows: rowsWithAsset,
        rowRects: r,
        pointerY: 72 + 5,
        active: { kind: "group", id: 11 },
        groups,
      })
    ).toEqual({ kind: "invalid" });
  });

  it("group 拖到另一 group header 上半区 → before-group（同级排序）", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 5, active: { kind: "group", id: 2 }, groups })
    ).toEqual({ kind: "before-group", groupID: 1, depth: 0 });
  });

  it("group 拖到自己或自己的子孙 → invalid", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 5, active: { kind: "group", id: 1 }, groups })
    ).toEqual({ kind: "invalid" });
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 18, active: { kind: "group", id: 1 }, groups })
    ).toEqual({ kind: "invalid" });
  });

  it("group 拖到 ungrouped 桶 header → invalid（group 不能嵌入未分组）", () => {
    const ungroupedRows: Row[] = [{ kind: "group-header", groupID: 0, depth: 0, collapsed: false }];
    const r = makeRects(ungroupedRows);
    expect(
      computeInsertionPoint({
        rows: ungroupedRows,
        rowRects: r,
        pointerY: 18,
        active: { kind: "group", id: 1 },
        groups,
      })
    ).toEqual({ kind: "invalid" });
  });
});

describe("computeInsertionPoint - empty placeholder & ungrouped", () => {
  const groups = [g(1)];
  const rows: Row[] = [
    { kind: "group-header", groupID: 1, depth: 0, collapsed: false },
    { kind: "empty-placeholder", groupID: 1, depth: 1 },
  ];
  const rects = makeRects(rows);

  it("empty-placeholder → into-group-first", () => {
    expect(
      computeInsertionPoint({ rows, rowRects: rects, pointerY: 24 + 12, active: { kind: "asset", id: 99 }, groups })
    ).toEqual({ kind: "into-group-first", groupID: 1, depth: 1 });
  });

  it("ungrouped 桶 header（任意半区，asset active）→ into-group-first", () => {
    const ungroupedRows: Row[] = [{ kind: "group-header", groupID: 0, depth: 0, collapsed: false }];
    const r = makeRects(ungroupedRows);
    expect(
      computeInsertionPoint({
        rows: ungroupedRows,
        rowRects: r,
        pointerY: 5,
        active: { kind: "asset", id: 99 },
        groups,
      })
    ).toEqual({ kind: "into-group-first", groupID: 0, depth: 0 });
  });
});
