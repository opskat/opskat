import { errorMessage } from "@/lib/errorMessage";
import { create } from "zustand";
import { persist } from "zustand/middleware";
import { asset_entity, group_entity } from "../../wailsjs/go/models";
import { ListAssets } from "../../wailsjs/go/system/System";
import {
  CreateAsset,
  UpdateAsset,
  DeleteAsset,
  DeleteAssets,
  ReorderAssets,
  GetAsset,
  ListGroups,
  CreateGroup,
  UpdateGroup,
  DeleteGroup,
} from "../../wailsjs/go/system/System";
import { useRecentAssetStore } from "./recentAssetStore";
import { formatAssetPath } from "@/lib/groupPath";

const EMPTY_ID_SET: ReadonlySet<number> = new Set();

function uniqueIds(ids: number[]): number[] {
  const seen = new Set<number>();
  const out: number[] = [];
  for (const id of ids) {
    if (seen.has(id)) continue;
    seen.add(id);
    out.push(id);
  }
  return out;
}

interface AssetState {
  assets: asset_entity.Asset[];
  groups: group_entity.Group[];
  selectedAssetId: number | null;
  selectedAssetIds: number[];
  selectedAssetIdSet: ReadonlySet<number>;
  selectionAnchorId: number | null;
  selectedGroupId: number | null;
  collapsedGroupIds: number[];
  loading: boolean;
  initialized: boolean;
  /** Why the last asset / group list load failed; each is cleared by its own next successful load. */
  loadErrors: { assets: string | null; groups: string | null };

  fetchAssets: (assetType?: string, groupId?: number) => Promise<void>;
  fetchGroups: () => Promise<void>;
  createAsset: (asset: asset_entity.Asset) => Promise<void>;
  updateAsset: (asset: asset_entity.Asset) => Promise<void>;
  deleteAsset: (id: number) => Promise<void>;
  deleteAssets: (ids: number[]) => Promise<void>;
  reorderAssets: (ids: number[], targetGroupID: number, beforeID: number) => Promise<void>;
  getAsset: (id: number) => Promise<asset_entity.Asset>;
  getAssetPath: (asset: asset_entity.Asset) => string;
  createGroup: (group: group_entity.Group) => Promise<void>;
  updateGroup: (group: group_entity.Group) => Promise<void>;
  deleteGroup: (id: number, deleteAssets: boolean) => Promise<void>;
  selectAsset: (id: number | null) => void;
  toggleAssetSelection: (id: number) => void;
  selectAssetRange: (visibleAssetIds: number[], targetId: number) => void;
  selectAllAssets: (visibleAssetIds: number[]) => void;
  clearSelection: () => void;
  pruneSelection: (visibleAssetIdSet: ReadonlySet<number>) => void;
  selectGroup: (id: number | null) => void;
  toggleGroupCollapsed: (id: number) => void;
  expandGroup: (id: number) => void;
  isGroupCollapsed: (id: number) => boolean;
  refresh: () => Promise<void>;
}

export const useAssetStore = create<AssetState>()(
  persist(
    (set, get) => ({
      assets: [],
      groups: [],
      selectedAssetId: null,
      selectedAssetIds: [],
      selectedAssetIdSet: EMPTY_ID_SET,
      selectionAnchorId: null,
      selectedGroupId: null,
      collapsedGroupIds: [],
      loading: false,
      initialized: false,
      loadErrors: { assets: null, groups: null },

      fetchAssets: async (assetType = "", groupId = 0) => {
        set({ loading: true });
        try {
          const assets = (await ListAssets(assetType, groupId)) || [];
          set((state) => {
            const hadUngroupedAssets = state.assets.some((asset) => !asset.GroupID);
            const hasUngroupedAssets = assets.some((asset) => !asset.GroupID);
            const existingIds = new Set(assets.map((asset) => asset.ID));
            const nextSelectedIds = state.selectedAssetIds.filter((id) => existingIds.has(id));
            const nextPrimary =
              state.selectedAssetId != null && existingIds.has(state.selectedAssetId)
                ? state.selectedAssetId
                : (nextSelectedIds[nextSelectedIds.length - 1] ?? null);
            const nextAnchor =
              state.selectionAnchorId != null && existingIds.has(state.selectionAnchorId)
                ? state.selectionAnchorId
                : (nextSelectedIds[nextSelectedIds.length - 1] ?? null);
            const selectionChanged =
              nextSelectedIds.length !== state.selectedAssetIds.length ||
              nextPrimary !== state.selectedAssetId ||
              nextAnchor !== state.selectionAnchorId;
            return {
              assets,
              initialized: true,
              loadErrors: { ...state.loadErrors, assets: null },
              ...(selectionChanged
                ? {
                    selectedAssetIds: nextSelectedIds,
                    selectedAssetIdSet: nextSelectedIds.length > 0 ? new Set(nextSelectedIds) : EMPTY_ID_SET,
                    selectedAssetId: nextPrimary,
                    selectionAnchorId: nextAnchor,
                  }
                : {}),
              collapsedGroupIds:
                state.initialized && !hadUngroupedAssets && hasUngroupedAssets
                  ? state.collapsedGroupIds.filter((id) => id !== 0)
                  : state.collapsedGroupIds,
            };
          });
        } catch (e) {
          set((state) => ({ loadErrors: { ...state.loadErrors, assets: errorMessage(e) } }));
          throw e;
        } finally {
          set({ loading: false });
        }
      },

      fetchGroups: async () => {
        try {
          const groups = await ListGroups();
          set((state) => ({ groups: groups || [], loadErrors: { ...state.loadErrors, groups: null } }));
        } catch (e) {
          set((state) => ({ loadErrors: { ...state.loadErrors, groups: errorMessage(e) } }));
          throw e;
        }
      },

      createAsset: async (asset) => {
        await CreateAsset(asset);
        await get().refresh();
      },

      updateAsset: async (asset) => {
        await UpdateAsset(asset);
        await get().refresh();
      },

      deleteAsset: async (id) => {
        await DeleteAsset(id);
        useRecentAssetStore.getState().remove(id);
        set({
          selectedAssetId: null,
          selectedAssetIds: [],
          selectedAssetIdSet: EMPTY_ID_SET,
          selectionAnchorId: null,
        });
        await get().refresh();
      },

      deleteAssets: async (ids) => {
        const unique = uniqueIds(ids);
        if (unique.length === 0) return;
        await DeleteAssets(unique);
        const recent = useRecentAssetStore.getState();
        for (const id of unique) recent.remove(id);
        const removed = new Set(unique);
        set((state) => {
          const nextIds = state.selectedAssetIds.filter((id) => !removed.has(id));
          return {
            selectedAssetIds: nextIds,
            selectedAssetIdSet: nextIds.length > 0 ? new Set(nextIds) : EMPTY_ID_SET,
            selectedAssetId:
              state.selectedAssetId != null && !removed.has(state.selectedAssetId)
                ? state.selectedAssetId
                : (nextIds[nextIds.length - 1] ?? null),
            selectionAnchorId:
              state.selectionAnchorId != null && !removed.has(state.selectionAnchorId)
                ? state.selectionAnchorId
                : (nextIds[nextIds.length - 1] ?? null),
          };
        });
        await get().refresh();
      },

      reorderAssets: async (ids, targetGroupID, beforeID) => {
        const unique = uniqueIds(ids);
        if (unique.length === 0) return;
        await ReorderAssets(unique, targetGroupID, beforeID);
        await get().refresh();
      },

      getAsset: async (id) => {
        return await GetAsset(id);
      },

      getAssetPath: (asset) => {
        const { groups } = get();
        return formatAssetPath(asset, groups, { separator: " / " });
      },

      createGroup: async (group) => {
        await CreateGroup(group);
        await get().fetchGroups();
      },

      updateGroup: async (group) => {
        await UpdateGroup(group);
        await get().fetchGroups();
      },

      deleteGroup: async (id, deleteAssets) => {
        await DeleteGroup(id, deleteAssets);
        await get().refresh();
      },

      selectAsset: (id) =>
        set(() => {
          if (id === null) {
            return {
              selectedAssetId: null,
              selectedAssetIds: [],
              selectedAssetIdSet: EMPTY_ID_SET,
              selectionAnchorId: null,
            };
          }
          return {
            selectedAssetId: id,
            selectedAssetIds: [id],
            selectedAssetIdSet: new Set([id]),
            selectionAnchorId: id,
          };
        }),

      toggleAssetSelection: (id) =>
        set((state) => {
          // 兼容直接 setState({ selectedAssetId }) 后的首次 Ctrl/Cmd+Click
          const baseIds =
            state.selectedAssetIds.length > 0
              ? state.selectedAssetIds
              : state.selectedAssetId != null
                ? [state.selectedAssetId]
                : [];
          const exists = baseIds.includes(id);
          const nextIds = exists ? baseIds.filter((item) => item !== id) : [...baseIds, id];
          return {
            selectedAssetIds: nextIds,
            selectedAssetIdSet: nextIds.length > 0 ? new Set(nextIds) : EMPTY_ID_SET,
            selectedAssetId: exists ? (nextIds[nextIds.length - 1] ?? null) : id,
            selectionAnchorId: id,
          };
        }),

      selectAssetRange: (visibleAssetIds, targetId) =>
        set((state) => {
          const anchor = state.selectionAnchorId ?? state.selectedAssetId;
          const from = anchor != null ? visibleAssetIds.indexOf(anchor) : -1;
          const to = visibleAssetIds.indexOf(targetId);
          if (from === -1 || to === -1) {
            return {
              selectedAssetId: targetId,
              selectedAssetIds: [targetId],
              selectedAssetIdSet: new Set([targetId]),
              selectionAnchorId: targetId,
            };
          }
          const range = uniqueIds(visibleAssetIds.slice(Math.min(from, to), Math.max(from, to) + 1));
          return {
            selectedAssetId: targetId,
            selectedAssetIds: range,
            selectedAssetIdSet: range.length > 0 ? new Set(range) : EMPTY_ID_SET,
            selectionAnchorId: anchor,
          };
        }),

      selectAllAssets: (visibleAssetIds) =>
        set(() => {
          const nextIds = uniqueIds(visibleAssetIds);
          return {
            selectedAssetIds: nextIds,
            selectedAssetIdSet: nextIds.length > 0 ? new Set(nextIds) : EMPTY_ID_SET,
            selectedAssetId: nextIds[0] ?? null,
            selectionAnchorId: nextIds[0] ?? null,
          };
        }),

      clearSelection: () =>
        set((state) => {
          if (
            state.selectedAssetIds.length === 0 &&
            state.selectedAssetId === null &&
            state.selectionAnchorId === null
          ) {
            return state;
          }
          return {
            selectedAssetId: null,
            selectedAssetIds: [],
            selectedAssetIdSet: EMPTY_ID_SET,
            selectionAnchorId: null,
          };
        }),

      pruneSelection: (visibleAssetIdSet) =>
        set((state) => {
          const nextIds = state.selectedAssetIds.filter((id) => visibleAssetIdSet.has(id));
          const nextPrimary =
            state.selectedAssetId != null && visibleAssetIdSet.has(state.selectedAssetId)
              ? state.selectedAssetId
              : (nextIds[nextIds.length - 1] ?? null);
          const nextAnchor =
            state.selectionAnchorId != null && visibleAssetIdSet.has(state.selectionAnchorId)
              ? state.selectionAnchorId
              : (nextIds[nextIds.length - 1] ?? null);
          if (
            nextIds.length === state.selectedAssetIds.length &&
            nextPrimary === state.selectedAssetId &&
            nextAnchor === state.selectionAnchorId
          ) {
            return state;
          }
          return {
            selectedAssetIds: nextIds,
            selectedAssetIdSet: nextIds.length > 0 ? new Set(nextIds) : EMPTY_ID_SET,
            selectedAssetId: nextPrimary,
            selectionAnchorId: nextAnchor,
          };
        }),

      selectGroup: (id) => set({ selectedGroupId: id }),

      toggleGroupCollapsed: (id) =>
        set((state) => {
          const exists = state.collapsedGroupIds.includes(id);
          return {
            collapsedGroupIds: exists
              ? state.collapsedGroupIds.filter((g) => g !== id)
              : [...state.collapsedGroupIds, id],
          };
        }),

      expandGroup: (id) =>
        set((state) => {
          // 同时展开该分组及其祖先链，确保新建子分组立即可见；带 visited 防环。
          const toExpand = new Set<number>([id]);
          const byId = new Map(state.groups.map((g) => [g.ID, g] as const));
          let cur = id;
          while (cur > 0) {
            const group = byId.get(cur);
            if (!group || !group.ParentID || toExpand.has(group.ParentID)) break;
            toExpand.add(group.ParentID);
            cur = group.ParentID;
          }
          const next = state.collapsedGroupIds.filter((gid) => !toExpand.has(gid));
          if (next.length === state.collapsedGroupIds.length) return state;
          return { collapsedGroupIds: next };
        }),

      isGroupCollapsed: (id) => get().collapsedGroupIds.includes(id),

      refresh: async () => {
        // 重试时先清掉原因,界面回到加载中,而不是在请求途中继续挂着上次的错误。
        set({ loadErrors: { assets: null, groups: null } });
        await Promise.all([get().fetchAssets(), get().fetchGroups()]);
      },
    }),
    {
      name: "asset_tree",
      partialize: (state) => ({ collapsedGroupIds: state.collapsedGroupIds }),
    }
  )
);

/** 资产树要显示的加载失败原因:资产列表或分组列表任一失败即算失败。 */
export const selectLoadError = (s: AssetState): string | null => s.loadErrors.assets ?? s.loadErrors.groups;
