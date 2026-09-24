// frontend/src/extension/store.ts
import { create } from "zustand";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "./assetTypes";
import type { ExtManifest, LoadedExtension } from "./types";

interface ExtensionEntry {
  manifest: ExtManifest;
  loaded?: LoadedExtension;
}

interface ExtensionState {
  ready: boolean;
  extensions: Record<string, ExtensionEntry>;
  /** 已安装但被禁用的扩展名。它们不在 extensions 里（类型/页面都不可用），
   * 单独记下来是为了让已打开的扩展页能明确显示"已禁用"，而不是等超时报"未注册"。 */
  disabled: Record<string, true>;
  setReady: (ready: boolean) => void;
  register: (name: string, manifest: ExtManifest) => void;
  unregister: (name: string) => void;
  markDisabled: (name: string) => void;
  setLoaded: (name: string, loaded: LoadedExtension) => void;
}

export const useExtensionStore = create<ExtensionState>((set) => ({
  ready: false,
  extensions: {},
  disabled: {},

  setReady(ready) {
    set({ ready });
  },

  register(name, manifest) {
    set((s) => {
      const { [name]: _, ...disabled } = s.disabled;
      return { extensions: { ...s.extensions, [name]: { manifest } }, disabled };
    });
    // 资产类型进的是内置类型那张注册表（见 ./assetTypes）。挂在这里而不是调用方，
    // 是为了让"扩展已加载"与"它的资产类型可用"永远同时成立——分开写迟早会漂移。
    registerExtensionAssetTypes(name, manifest);
  },

  unregister(name) {
    set((s) => {
      const { [name]: _, ...rest } = s.extensions;
      const { [name]: __, ...disabled } = s.disabled;
      return { extensions: rest, disabled };
    });
    unregisterExtensionAssetTypes(name);
  },

  markDisabled(name) {
    set((s) => {
      const { [name]: _, ...rest } = s.extensions;
      return { extensions: rest, disabled: { ...s.disabled, [name]: true } };
    });
    unregisterExtensionAssetTypes(name);
  },

  setLoaded(name, loaded) {
    set((s) => {
      const entry = s.extensions[name];
      if (!entry) return s;
      return { extensions: { ...s.extensions, [name]: { ...entry, loaded } } };
    });
  },
}));
