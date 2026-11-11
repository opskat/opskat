import { errorMessage } from "@/lib/errorMessage";
import { create } from "zustand";
import { persist } from "zustand/middleware";
import { asset_entity, group_entity } from "../../wailsjs/go/models";
import { ListAssets } from "../../wailsjs/go/system/System";
import {
  CreateAsset,
  UpdateAsset,
  DeleteAsset,
  GetAsset,
  ListGroups,
  CreateGroup,
  UpdateGroup,
  DeleteGroup,
} from "../../wailsjs/go/system/System";
import { useRecentAssetStore } from "./recentAssetStore";
import { formatAssetPath } from "@/lib/groupPath";

interface AssetState {
  assets: asset_entity.Asset[];
  groups: group_entity.Group[];
  selectedAssetId: number | null;
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
  getAsset: (id: number) => Promise<asset_entity.Asset>;
  getAssetPath: (asset: asset_entity.Asset) => string;
  createGroup: (group: group_entity.Group) => Promise<void>;
  updateGroup: (group: group_entity.Group) => Promise<void>;
  deleteGroup: (id: number, deleteAssets: boolean) => Promise<void>;
  selectAsset: (id: number | null) => void;
  selectGroup: (id: number | null) => void;
  toggleGroupCollapsed: (id: number) => void;
  isGroupCollapsed: (id: number) => boolean;
  refresh: () => Promise<void>;
}

export const useAssetStore = create<AssetState>()(
  persist(
    (set, get) => ({
      assets: [],
      groups: [],
      selectedAssetId: null,
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
            return {
              assets,
              initialized: true,
              loadErrors: { ...state.loadErrors, assets: null },
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
        set({ selectedAssetId: null });
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

      selectAsset: (id) => set({ selectedAssetId: id }),
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
