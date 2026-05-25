import { asset_entity, group_entity } from "../../../wailsjs/go/models";

export type Row =
  | { kind: "group-header"; groupID: number; depth: number; collapsed: boolean }
  | { kind: "asset"; assetID: number; groupID: number; depth: number }
  | { kind: "empty-placeholder"; groupID: number; depth: number };

export interface FlattenInput {
  groups: group_entity.Group[];
  assets: asset_entity.Asset[];
  collapsedGroupIDs: Set<number>;
  shouldHideEmpty: boolean;
}

export function flattenTree({ groups, assets, collapsedGroupIDs, shouldHideEmpty }: FlattenInput): Row[] {
  const childrenByParent = new Map<number, group_entity.Group[]>();
  for (const g of groups) {
    const p = g.ParentID ?? 0;
    if (!childrenByParent.has(p)) childrenByParent.set(p, []);
    childrenByParent.get(p)!.push(g);
  }
  const assetsByGroup = new Map<number, asset_entity.Asset[]>();
  for (const a of assets) {
    const gid = a.GroupID ?? 0;
    if (!assetsByGroup.has(gid)) assetsByGroup.set(gid, []);
    assetsByGroup.get(gid)!.push(a);
  }

  const countAssetsInGroup = (groupID: number): number => {
    let n = (assetsByGroup.get(groupID) || []).length;
    for (const c of childrenByParent.get(groupID) || []) n += countAssetsInGroup(c.ID);
    return n;
  };

  const rows: Row[] = [];
  const walk = (parentID: number, depth: number) => {
    for (const grp of childrenByParent.get(parentID) || []) {
      if (shouldHideEmpty && countAssetsInGroup(grp.ID) === 0) continue;
      const collapsed = collapsedGroupIDs.has(grp.ID);
      rows.push({ kind: "group-header", groupID: grp.ID, depth, collapsed });
      if (collapsed) continue;
      const before = rows.length;
      walk(grp.ID, depth + 1);
      for (const a of assetsByGroup.get(grp.ID) || []) {
        rows.push({ kind: "asset", assetID: a.ID, groupID: grp.ID, depth: depth + 1 });
      }
      if (rows.length === before) {
        rows.push({ kind: "empty-placeholder", groupID: grp.ID, depth: depth + 1 });
      }
    }
  };
  walk(0, 0);

  const rootAssets = assetsByGroup.get(0) || [];
  if (rootAssets.length > 0) {
    const collapsed = collapsedGroupIDs.has(0);
    rows.push({ kind: "group-header", groupID: 0, depth: 0, collapsed });
    if (!collapsed) {
      for (const a of rootAssets) {
        rows.push({ kind: "asset", assetID: a.ID, groupID: 0, depth: 1 });
      }
    }
  }
  return rows;
}
