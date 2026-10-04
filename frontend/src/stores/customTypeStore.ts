import { useState } from "react";
import { toast } from "sonner";
import { create } from "zustand";
import {
  ListCustomTypes,
  GetCustomType,
  GetCustomTypeUsage,
  SaveCustomType,
  DeleteCustomType,
} from "../../wailsjs/go/customtype/CustomType";
import type { customtype, custom_type_entity } from "../../wailsjs/go/models";
import { useAssetStore } from "./assetStore";

interface CustomTypeState {
  types: customtype.Summary[];
  loading: boolean;
  loaded: boolean;

  load: () => Promise<void>;
  get: (id: number) => Promise<custom_type_entity.CustomType>;
  usage: (id: number) => Promise<string[]>;
  save: (ct: custom_type_entity.CustomType) => Promise<customtype.SaveResult>;
  remove: (id: number) => Promise<customtype.DeleteResult>;
}

// 资产变化(opsctl/AI data:changed、备份导入、界面增删改都经 useAssetStore.refresh)会改动类型 ID 与"N 个资产"计数；
// 首次加载成功后才开始跟随(导入模块无副作用、首次使用前不抢跑)，失败与首次加载一样 toast。
let followingAssets = false;
function followAssetChanges() {
  if (followingAssets) return;
  followingAssets = true;
  useAssetStore.subscribe((state, prev) => {
    if (state.assets === prev.assets) return;
    const { loaded, load } = useCustomTypeStore.getState();
    if (!loaded) return;
    load().catch((e) => toast.error(String(e)));
  });
}

// 只存列表快照 + 加载态；对话框的打开/草稿状态留在组件里(与 AgentSourceDialog / CredentialManager 同款)。
export const useCustomTypeStore = create<CustomTypeState>((set, get) => ({
  types: [],
  loading: false,
  loaded: false,

  async load() {
    set({ loading: true });
    try {
      const list = await ListCustomTypes();
      set({ types: list ?? [], loaded: true });
      followAssetChanges();
    } finally {
      set({ loading: false });
    }
  },

  async get(id) {
    return GetCustomType(id);
  },

  async usage(id) {
    return GetCustomTypeUsage(id);
  },

  async save(ct) {
    const res = await SaveCustomType(ct);
    if (res.type) {
      await get().load();
    }
    return res;
  },

  async remove(id) {
    const res = await DeleteCustomType(id);
    if (res.deleted) {
      await get().load();
    }
    return res;
  },
}));

/**
 * 订阅自定义类型列表快照；列表还没加载、也没在加载时，首次渲染触发一次加载，失败 toast。
 * 各处列表消费方共用这一个"只加载一次"，不各写一份。
 */
export function useCustomTypeList(): { types: customtype.Summary[]; loaded: boolean; loading: boolean } {
  const types = useCustomTypeStore((s) => s.types);
  const loaded = useCustomTypeStore((s) => s.loaded);
  const loading = useCustomTypeStore((s) => s.loading);
  const load = useCustomTypeStore((s) => s.load);
  const [requested, setRequested] = useState(false);
  if (!requested && !loaded && !loading) {
    setRequested(true);
    load().catch((e) => toast.error(String(e)));
  }
  return { types, loaded, loading };
}
