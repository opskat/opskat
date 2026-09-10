import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { AssetTypeDefinition } from "./types";

interface AssetTypeRegistryState {
  /** type → definition，按注册顺序（选择器分组内的展示顺序依赖它）。 */
  types: Record<string, AssetTypeDefinition>;
}

/**
 * 资产类型注册表。
 *
 * 它是一个 store 而不是裸 Map，因为条目会在运行期增删：扩展提供的资产类型随扩展启用/
 * 禁用来去，读到它们的组件（类型选择器、资产树筛选、详情页、表单）必须跟着重渲染。
 *
 * 选择"注册表本身就是 store"而不是"在 extension store 里放一个 version 计数器"：后者
 * 让组件订阅另一个领域 store 里的计数来得知**本表**的内容变了，两者可以各自漂移
 * （改了表没 bump、bump 了表没改），而且对所有 `getAssetType(...)` 这类一次性读取毫无
 * 帮助。内容与订阅同源才不会分叉。
 */
export const useAssetTypeRegistry = create<AssetTypeRegistryState>(() => ({ types: {} }));

export function registerAssetType(def: AssetTypeDefinition) {
  useAssetTypeRegistry.setState((s) => ({ types: { ...s.types, [def.type]: def } }));
}

/** 注销一个资产类型（扩展禁用/卸载）。未注册的类型是 no-op。 */
export function unregisterAssetType(type: string) {
  useAssetTypeRegistry.setState((s) => {
    if (!(type in s.types)) return s;
    const { [type]: _removed, ...rest } = s.types;
    return { types: rest };
  });
}

/** 订阅全部资产类型定义（按注册顺序）。内容不变时不触发重渲染。 */
export function useAssetTypes(): AssetTypeDefinition[] {
  return useAssetTypeRegistry(useShallow((s) => Object.values(s.types)));
}

/** 订阅单个资产类型定义；未注册返回 undefined。 */
export function useAssetTypeDef(type: string): AssetTypeDefinition | undefined {
  return useAssetTypeRegistry((s) => s.types[type]);
}
