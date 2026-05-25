import { create } from "zustand";
import type { InsertionPoint } from "./insertionPoint";

interface IndicatorState {
  point: InsertionPoint | null;
  indicatorY: number | null;
  indicatorDepth: number | null;
  highlightedGroupID: number | null;
  setIndicator: (
    point: InsertionPoint | null,
    indicatorY: number | null,
    indicatorDepth: number | null,
    highlightedGroupID: number | null
  ) => void;
  clear: () => void;
}

export const useAssetTreeDndStore = create<IndicatorState>((set) => ({
  point: null,
  indicatorY: null,
  indicatorDepth: null,
  highlightedGroupID: null,
  setIndicator: (point, indicatorY, indicatorDepth, highlightedGroupID) =>
    set((s) => {
      if (
        s.indicatorY === indicatorY &&
        s.indicatorDepth === indicatorDepth &&
        s.highlightedGroupID === highlightedGroupID &&
        s.point?.kind === point?.kind
      ) {
        return s;
      }
      return { point, indicatorY, indicatorDepth, highlightedGroupID };
    }),
  clear: () => set({ point: null, indicatorY: null, indicatorDepth: null, highlightedGroupID: null }),
}));
