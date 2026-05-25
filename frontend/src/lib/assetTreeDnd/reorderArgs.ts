import { asset_entity, group_entity } from "../../../wailsjs/go/models";
import type { InsertionPoint } from "./insertionPoint";

export type ReorderArgs =
  | { kind: "asset"; id: number; targetGroupID: number; beforeID: number }
  | { kind: "group"; id: number; targetParentID: number; beforeID: number }
  | null;

export interface InsertionToReorderArgsInput {
  point: InsertionPoint;
  active: { kind: "asset" | "group"; id: number };
  groups: group_entity.Group[];
  assets: asset_entity.Asset[];
}

export function insertionToReorderArgs({ point, active, groups, assets }: InsertionToReorderArgsInput): ReorderArgs {
  if (point.kind === "invalid") return null;

  if (active.kind === "asset") {
    return computeAssetArgs(point, active.id, groups, assets);
  }
  return computeGroupArgs(point, active.id, groups);
}

function computeAssetArgs(
  point: InsertionPoint,
  activeID: number,
  groups: group_entity.Group[],
  assets: asset_entity.Asset[]
): ReorderArgs {
  switch (point.kind) {
    case "before-asset":
      return { kind: "asset", id: activeID, targetGroupID: point.groupID, beforeID: point.assetID };
    case "after-asset": {
      const siblings = assets.filter((a) => (a.GroupID ?? 0) === point.groupID && a.ID !== activeID);
      const idx = siblings.findIndex((s) => s.ID === point.assetID);
      const beforeID = idx >= 0 ? (siblings[idx + 1]?.ID ?? 0) : 0;
      return { kind: "asset", id: activeID, targetGroupID: point.groupID, beforeID };
    }
    case "before-group": {
      const grp = groups.find((g) => g.ID === point.groupID);
      const targetGroupID = grp?.ParentID ?? 0;
      return { kind: "asset", id: activeID, targetGroupID, beforeID: 0 };
    }
    case "into-group-first": {
      const siblings = assets.filter((a) => (a.GroupID ?? 0) === point.groupID && a.ID !== activeID);
      const beforeID = siblings[0]?.ID ?? 0;
      return { kind: "asset", id: activeID, targetGroupID: point.groupID, beforeID };
    }
    case "root-end":
      return { kind: "asset", id: activeID, targetGroupID: 0, beforeID: 0 };
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
