import { asset_entity, group_entity } from "../../../wailsjs/go/models";
import type { DragActive, InsertionPoint } from "./insertionPoint";

export type ReorderArgs =
  | { kind: "asset"; id: number; ids: number[]; targetGroupID: number; beforeID: number }
  | { kind: "group"; id: number; targetParentID: number; beforeID: number }
  | null;

export interface InsertionToReorderArgsInput {
  point: InsertionPoint;
  active: DragActive;
  groups: group_entity.Group[];
  assets: asset_entity.Asset[];
  /** 筛选模式下拖入分组直接追加到目标组末尾（beforeID=0），避免依赖不可见兄弟项。 */
  isFiltered?: boolean;
}

export function insertionToReorderArgs({
  point,
  active,
  groups,
  assets,
  isFiltered = false,
}: InsertionToReorderArgsInput): ReorderArgs {
  if (point.kind === "invalid") return null;

  if (active.kind === "asset") {
    const ids = active.ids && active.ids.length > 0 ? active.ids : [active.id];
    return computeAssetArgs(point, ids, groups, assets, isFiltered);
  }
  return computeGroupArgs(point, active.id, groups);
}

function computeAssetArgs(
  point: InsertionPoint,
  activeIDs: number[],
  groups: group_entity.Group[],
  assets: asset_entity.Asset[],
  isFiltered: boolean
): ReorderArgs {
  const leadID = activeIDs[0];
  const movedSet = new Set(activeIDs);
  switch (point.kind) {
    case "before-asset":
      return { kind: "asset", id: leadID, ids: activeIDs, targetGroupID: point.groupID, beforeID: point.assetID };
    case "after-asset": {
      const siblings = assets.filter((a) => (a.GroupID ?? 0) === point.groupID && !movedSet.has(a.ID));
      const idx = siblings.findIndex((s) => s.ID === point.assetID);
      const beforeID = idx >= 0 ? (siblings[idx + 1]?.ID ?? 0) : 0;
      return { kind: "asset", id: leadID, ids: activeIDs, targetGroupID: point.groupID, beforeID };
    }
    case "before-group":
    case "after-group": {
      const grp = groups.find((g) => g.ID === point.groupID);
      const targetGroupID = grp?.ParentID ?? 0;
      return { kind: "asset", id: leadID, ids: activeIDs, targetGroupID, beforeID: 0 };
    }
    case "into-group-first": {
      if (isFiltered) {
        return { kind: "asset", id: leadID, ids: activeIDs, targetGroupID: point.groupID, beforeID: 0 };
      }
      const siblings = assets.filter((a) => (a.GroupID ?? 0) === point.groupID && !movedSet.has(a.ID));
      const beforeID = siblings[0]?.ID ?? 0;
      return { kind: "asset", id: leadID, ids: activeIDs, targetGroupID: point.groupID, beforeID };
    }
    case "root-end":
      return { kind: "asset", id: leadID, ids: activeIDs, targetGroupID: 0, beforeID: 0 };
    case "invalid":
      return null;
  }
}

function computeGroupArgs(point: InsertionPoint, activeID: number, groups: group_entity.Group[]): ReorderArgs {
  switch (point.kind) {
    case "before-asset":
    case "after-asset":
      return null;
    case "before-group": {
      const grp = groups.find((g) => g.ID === point.groupID);
      const targetParentID = grp?.ParentID ?? 0;
      return { kind: "group", id: activeID, targetParentID, beforeID: point.groupID };
    }
    case "after-group": {
      const grp = groups.find((g) => g.ID === point.groupID);
      const targetParentID = grp?.ParentID ?? 0;
      const siblings = groups.filter((g) => (g.ParentID ?? 0) === targetParentID && g.ID !== activeID);
      const idx = siblings.findIndex((s) => s.ID === point.groupID);
      const beforeID = idx >= 0 ? (siblings[idx + 1]?.ID ?? 0) : 0;
      return { kind: "group", id: activeID, targetParentID, beforeID };
    }
    case "into-group-first": {
      const targetParentID = point.groupID;
      const childGroups = groups.filter((g) => (g.ParentID ?? 0) === targetParentID && g.ID !== activeID);
      const beforeID = childGroups[0]?.ID ?? 0;
      return { kind: "group", id: activeID, targetParentID, beforeID };
    }
    case "root-end":
      return { kind: "group", id: activeID, targetParentID: 0, beforeID: 0 };
    case "invalid":
      return null;
  }
}
