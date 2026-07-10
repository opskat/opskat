import { create } from "zustand";

interface RDPState {
  activeAssetIds: Set<number>;
  setAssetConnected: (assetId: number, connected: boolean) => void;
}

export const useRDPStore = create<RDPState>((set) => ({
  activeAssetIds: new Set(),
  setAssetConnected: (assetId, connected) =>
    set((state) => {
      const activeAssetIds = new Set(state.activeAssetIds);
      if (connected) activeAssetIds.add(assetId);
      else activeAssetIds.delete(assetId);
      return { activeAssetIds };
    }),
}));

export function getRDPActiveAssetIds(): Set<number> {
  return useRDPStore.getState().activeAssetIds;
}
