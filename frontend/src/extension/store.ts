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
  /** 丢弃全部已加载的前端包（扩展重装/重载后，下次打开页面重新加载）。 */
  clearLoaded: () => void;
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
      // 重新注册（如切换语言后换上新语言的 manifest）不换前端包：已加载的保留，
      // 打开着的扩展页不因此重载。换包只走 clearLoaded（扩展重装/重载）。
      const loaded = s.extensions[name]?.loaded;
      return { extensions: { ...s.extensions, [name]: loaded ? { manifest, loaded } : { manifest } }, disabled };
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

  clearLoaded() {
    set((s) => ({
      extensions: Object.fromEntries(Object.entries(s.extensions).map(([name, { manifest }]) => [name, { manifest }])),
    }));
  },

  setLoaded(name, loaded) {
    set((s) => {
      const entry = s.extensions[name];
      if (!entry) return s;
      return { extensions: { ...s.extensions, [name]: { ...entry, loaded } } };
    });
  },
}));
