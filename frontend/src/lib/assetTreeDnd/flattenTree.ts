import { asset_entity, group_entity } from "../../../wailsjs/go/models";

export type Row =
  | { kind: "group-header"; groupID: number; depth: number; collapsed: boolean }
  | { kind: "asset"; assetID: number; groupID: number; depth: number }
  | { kind: "empty-placeholder"; groupID: number; depth: number };

/** Lookups over the groups and the (filtered) assets the tree shows, built in one O(G+A) pass. */
export interface TreeIndex {
  /** Child groups per ParentID (0 = root), in list order. */
  childGroups: Map<number, group_entity.Group[]>;
  /** Assets per GroupID (0 = ungrouped). */
  assetsByGroup: Map<number, asset_entity.Asset[]>;
  /** Assets in a group and all its descendants; 0 is the ungrouped header (root assets only). */
  totalCounts: Map<number, number>;
  /** Number of descendant groups, used by the delete confirmation. */
  descendantGroupCounts: Map<number, number>;
}

const NO_GROUPS: group_entity.Group[] = [];

export function buildTreeIndex(groups: group_entity.Group[], assets: asset_entity.Asset[]): TreeIndex {
  const assetsByGroup = new Map<number, asset_entity.Asset[]>();
  for (const asset of assets) {
    const gid = asset.GroupID || 0;
    const list = assetsByGroup.get(gid);
    if (list) list.push(asset);
    else assetsByGroup.set(gid, [asset]);
  }
  const childGroups = new Map<number, group_entity.Group[]>();
  for (const group of groups) {
    const pid = group.ParentID || 0;
    const list = childGroups.get(pid);
    if (list) list.push(group);
    else childGroups.set(pid, [group]);
  }
  const totalCounts = new Map<number, number>();
  const descendantGroupCounts = new Map<number, number>();
  const visit = (id: number) => {
    if (totalCounts.has(id)) return;
    let total = assetsByGroup.get(id)?.length ?? 0;
    let descendants = 0;
    for (const child of childGroups.get(id) ?? NO_GROUPS) {
      visit(child.ID);
      total += totalCounts.get(child.ID)!;
      descendants += 1 + descendantGroupCounts.get(child.ID)!;
    }
    totalCounts.set(id, total);
    descendantGroupCounts.set(id, descendants);
  };
  for (const group of groups) visit(group.ID);
  totalCounts.set(0, assetsByGroup.get(0)?.length ?? 0);
  return { childGroups, assetsByGroup, totalCounts, descendantGroupCounts };
}

export interface FlattenInput {
  index: TreeIndex;
  collapsedGroupIDs: Set<number>;
  shouldHideEmpty: boolean;
}

export function flattenTree({ index, collapsedGroupIDs, shouldHideEmpty }: FlattenInput): Row[] {
  const { childGroups, assetsByGroup, totalCounts } = index;
  const rows: Row[] = [];
  const walk = (parentID: number, depth: number) => {
    for (const grp of childGroups.get(parentID) ?? NO_GROUPS) {
      if (shouldHideEmpty && totalCounts.get(grp.ID) === 0) continue;
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
