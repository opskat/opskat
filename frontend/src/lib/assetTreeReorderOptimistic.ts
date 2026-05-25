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
  const moving = assets.find((asset) => asset.ID === movedID);
  if (!moving) return assets;

  const moved = { ...moving, GroupID: targetGroupID } as T;
  const withoutMoved = assets.filter((asset) => asset.ID !== movedID);
  const targetSiblings = withoutMoved.filter((asset) => (asset.GroupID || 0) === targetGroupID);
  const orderedTarget: T[] = [];
  let inserted = false;

  for (const asset of targetSiblings) {
    if (!inserted && beforeID !== 0 && asset.ID === beforeID) {
      orderedTarget.push(moved);
      inserted = true;
    }
    orderedTarget.push(asset);
  }
  if (!inserted) orderedTarget.push(moved);

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
