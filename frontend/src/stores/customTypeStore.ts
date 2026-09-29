import { create } from "zustand";
import {
  ListCustomTypes,
  GetCustomType,
  GetCustomTypeUsage,
  SaveCustomType,
  DeleteCustomType,
} from "../../wailsjs/go/customtype/CustomType";
import type { customtype, custom_type_entity } from "../../wailsjs/go/models";

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
