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
  /** 最近一次加载失败的原因;成功后清空。依赖列表才能渲染的地方据此显示原因而不是一直等。 */
  loadError: string;

  load: () => Promise<void>;
  get: (id: number) => Promise<custom_type_entity.CustomType>;
  usage: (id: number) => Promise<string[]>;
  save: (ct: custom_type_entity.CustomType) => Promise<customtype.SaveResult>;
  remove: (id: number) => Promise<customtype.DeleteResult>;
}

// 资产变化(opsctl/AI data:changed、备份导入、界面增删改都经 useAssetStore.refresh)会改动类型 ID 与"N 个资产"计数；
// 首次加载成功后才开始跟随(导入模块无副作用、首次使用前不抢跑)，失败与首次加载一样 toast。
let followingAssets = false;
// 本 store 自己在刷新资产时(见 reloadAfterWrite)已经显式加载了列表,暂停跟随,同一份列表不加载两次。
let ownAssetRefresh = false;
function followAssetChanges() {
  if (followingAssets) return;
  followingAssets = true;
  useAssetStore.subscribe((state, prev) => {
    if (ownAssetRefresh || state.assets === prev.assets) return;
    const { loaded, load } = useCustomTypeStore.getState();
    if (!loaded) return;
    load().catch((e) => toast.error(String(e)));
  });
}

/**
 * 写入成功后换掉快照。写入本身已经成功,这里失败不能再报成"保存 / 删除失败"(编辑器会停在未保存
 * 状态,再点一次就是标识重名),所以单独 toast。withAssets:更新类型时后端会改写该类型的资产
 * (删除字段的值、密钥属性变了的值重存),资产快照必须跟着换——否则下一次保存资产(详情页改策略、
 * 资产表单保存)会把旧 Config 整行写回去。
 */
async function reloadAfterWrite(load: () => Promise<void>, withAssets: boolean) {
  ownAssetRefresh = true;
  try {
    await Promise.all([load(), withAssets ? useAssetStore.getState().refresh() : undefined]);
  } catch (e) {
    toast.error(String(e));
  } finally {
    ownAssetRefresh = false;
  }
}

// 只存列表快照 + 加载态；对话框的打开/草稿状态留在组件里(与 AgentSourceDialog / CredentialManager 同款)。
export const useCustomTypeStore = create<CustomTypeState>((set, get) => ({
  types: [],
  loading: false,
  loaded: false,
  loadError: "",

  async load() {
    set({ loading: true });
    try {
      const list = await ListCustomTypes();
      set({ types: list ?? [], loaded: true, loadError: "" });
      followAssetChanges();
    } catch (e) {
      set({ loadError: String(e) });
      throw e;
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
      await reloadAfterWrite(get().load, true);
    }
    return res;
  },

  async remove(id) {
    const res = await DeleteCustomType(id);
    if (res.deleted) {
      await reloadAfterWrite(get().load, false);
    }
    return res;
  },
}));

/**
 * 订阅自定义类型列表快照；列表还没加载、也没在加载时，首次渲染触发一次加载，失败 toast。
 * 各处列表消费方共用这一个"只加载一次"，不各写一份。
 */
export function useCustomTypeList(): {
  types: customtype.Summary[];
  loaded: boolean;
  loading: boolean;
  loadError: string;
} {
  const types = useCustomTypeStore((s) => s.types);
  const loaded = useCustomTypeStore((s) => s.loaded);
  const loading = useCustomTypeStore((s) => s.loading);
  const loadError = useCustomTypeStore((s) => s.loadError);
  const load = useCustomTypeStore((s) => s.load);
  const [requested, setRequested] = useState(false);
  if (!requested && !loaded && !loading) {
    setRequested(true);
    load().catch((e) => toast.error(String(e)));
  }
  return { types, loaded, loading, loadError };
}
