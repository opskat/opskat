import { create } from "zustand";
import type { InsertionPoint } from "./insertionPoint";

const EMPTY_ID_SET: ReadonlySet<number> = new Set();

interface IndicatorState {
  point: InsertionPoint | null;
  indicatorY: number | null;
  indicatorDepth: number | null;
  highlightedGroupID: number | null;
  draggingAssetIDSet: ReadonlySet<number>;
  setIndicator: (
    point: InsertionPoint | null,
    indicatorY: number | null,
    indicatorDepth: number | null,
    highlightedGroupID: number | null
  ) => void;
  setDraggingAssetIDs: (ids: number[]) => void;
  clear: () => void;
}

export const useAssetTreeDndStore = create<IndicatorState>((set) => ({
  point: null,
  indicatorY: null,
  indicatorDepth: null,
  highlightedGroupID: null,
  draggingAssetIDSet: EMPTY_ID_SET,
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
  setDraggingAssetIDs: (ids) => set({ draggingAssetIDSet: ids.length > 0 ? new Set(ids) : EMPTY_ID_SET }),
  clear: () =>
    set({
      point: null,
      indicatorY: null,
      indicatorDepth: null,
      highlightedGroupID: null,
      draggingAssetIDSet: EMPTY_ID_SET,
    }),
}));
