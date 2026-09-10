import type { AssetTypeDefinition } from "./types";
import { useAssetTypeRegistry } from "./_register";
export { registerAssetType, unregisterAssetType, useAssetTypes, useAssetTypeDef } from "./_register";

export function getAssetType(type: string): AssetTypeDefinition | undefined {
  return useAssetTypeRegistry.getState().types[type];
}

/**
 * 是否是已注册的资产类型。内置类型在模块加载时注册，扩展提供的类型在扩展加载后注册，
 * 因此对同一个类型的答案可能从 false 变成 true —— 调用方要么走响应式的 useAssetTypeDef，
 * 要么接受"还没注册"这个中间态。
 */
export function isKnownAssetType(type: string): boolean {
  return type in useAssetTypeRegistry.getState().types;
}

export function getAllAssetTypes(): AssetTypeDefinition[] {
  return Object.values(useAssetTypeRegistry.getState().types);
}

/**
 * connectAction === "page" 类型的 tab id 前缀。默认取 pageId；类型可用 pageTabPrefix
 * 覆盖（k8s 的 tab id 是历史形态 `k8s-<id>`，而它渲染的页面是 "k8s-cluster"）。
 */
export function pageTabPrefix(def: AssetTypeDefinition): string {
  return def.pageTabPrefix ?? def.pageId ?? def.type;
}

// Side-effect imports — register all built-in types
import "./ssh";
import "./database";
import "./redis";
import "./mongodb";
import "./kafka";
import "./k8s";
import "./serial";
import "./local";
import "./vnc";
import "./rdp";
import "./etcd";
import "./oss";

export type { AssetTypeDefinition, DetailInfoCardProps, PolicyDefinition, PolicyFieldDef } from "./types";
