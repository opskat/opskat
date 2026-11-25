export interface OptimisticAsset {
  ID: number;
  GroupID?: number;
  SortOrder?: number;
}

export function reorderAssetsOptimistically<T extends OptimisticAsset>(
  assets: T[],
  movedID: number,
  targetGroupID: number,
  beforeID: number
): T[] {
  return reorderAssetsBatchOptimistically(assets, [movedID], targetGroupID, beforeID);
}

export function reorderAssetsBatchOptimistically<T extends OptimisticAsset>(
  assets: T[],
  movedIDs: number[],
  targetGroupID: number,
  beforeID: number
): T[] {
  const byID = new Map(assets.map((asset) => [asset.ID, asset] as const));
  const movedSet = new Set<number>();
  const movedList: T[] = [];
  for (const id of movedIDs) {
    if (movedSet.has(id)) continue;
    const found = byID.get(id);
    if (!found) continue;
    movedSet.add(id);
    movedList.push({ ...found, GroupID: targetGroupID } as T);
  }
  if (movedList.length === 0) return assets;

  let effectiveBeforeID = beforeID;
  if (effectiveBeforeID !== 0 && movedSet.has(effectiveBeforeID)) {
    const siblingsInTarget = assets.filter((asset) => (asset.GroupID || 0) === targetGroupID);
    const anchorIdx = siblingsInTarget.findIndex((asset) => asset.ID === beforeID);
    effectiveBeforeID = 0;
    if (anchorIdx >= 0) {
      for (let i = anchorIdx + 1; i < siblingsInTarget.length; i++) {
        if (!movedSet.has(siblingsInTarget[i].ID)) {
          effectiveBeforeID = siblingsInTarget[i].ID;
          break;
        }
      }
    }
  }

  const withoutMoved = assets.filter((asset) => !movedSet.has(asset.ID));
  const targetSiblings = withoutMoved.filter((asset) => (asset.GroupID || 0) === targetGroupID);
  const orderedTarget: T[] = [];
  let inserted = false;

  for (const asset of targetSiblings) {
    if (!inserted && effectiveBeforeID !== 0 && asset.ID === effectiveBeforeID) {
      orderedTarget.push(...movedList);
      inserted = true;
    }
    orderedTarget.push(asset);
  }
  if (!inserted) orderedTarget.push(...movedList);

  const normalizedTarget = orderedTarget.map((asset, index) => ({
    ...asset,
    GroupID: targetGroupID,
    SortOrder: (index + 1) * 10,
  })) as T[];

  const next: T[] = [];
  let targetBlockInserted = false;
  for (const asset of withoutMoved) {
    if ((asset.GroupID || 0) === targetGroupID) {
      if (!targetBlockInserted) {
        next.push(...normalizedTarget);
        targetBlockInserted = true;
      }
      continue;
    }
    next.push(asset);
  }

  if (!targetBlockInserted) next.push(...normalizedTarget);
  return next;
}
