// frontend/src/lib/assetTypes/options.ts
import type { ComponentType } from "react";
import { getAllAssetTypes, useAssetTypes } from "./index";
import type { AssetTypeCategory, AssetTypeDefinition } from "./types";
import type { asset_entity } from "../../../wailsjs/go/models";

export type { AssetTypeCategory };

/** 翻译函数（可带 i18next 命名空间）；兼容 react-i18next 的 t。 */
export type TranslateFn = (key: string, opts?: { ns?: string }) => string;

export interface AssetTypeOption {
  /** Stable identifier — used as the persisted "selected" value. */
  value: string;
  /** All `asset.Type` values that should match when this option is selected. */
  aliases: string[];
  /** i18n key (built-in → default namespace; extension → `i18nNs`) or a literal display string. */
  label: string;
  /** Marks `label` as i18n key vs literal. */
  labelIsI18nKey: boolean;
  /** i18next namespace for resolving `label` (extensions load under `ext-<name>`); omit for the default namespace. */
  i18nNs?: string;
  /** Icon component for direct render. */
  icon: ComponentType<{ className?: string; style?: React.CSSProperties }>;
  group: "builtin" | "extension";
  /** 语义分组（选择器展示用）。 */
  category: AssetTypeCategory;
}

function toOption(def: AssetTypeDefinition): AssetTypeOption {
  return {
    value: def.type,
    aliases: def.aliases,
    label: def.label,
    labelIsI18nKey: true,
    i18nNs: def.labelNs,
    icon: def.icon,
    group: def.extensionName ? "extension" : "builtin",
    category: def.category,
  };
}

/**
 * 全部资产类型选项，从注册表派生（单一来源）。
 *
 * 它曾经额外收一个 extensions 参数，把扩展类型现场拼成 option —— 那是"注册表之外的第二
 * 份类型清单"。扩展类型现在也在注册表里，参数因此消失了。
 */
export function getAssetTypeOptions(): AssetTypeOption[] {
  return getAllAssetTypes().map(toOption);
}

/** 响应式版本：注册表增删（扩展启用/禁用）时组件会重渲染。 */
export function useAssetTypeOptions(): AssetTypeOption[] {
  return useAssetTypes().map(toOption);
}

export function matchSelectedTypes(
  assets: asset_entity.Asset[],
  selectedTypes: string[],
  options: AssetTypeOption[]
): asset_entity.Asset[] {
  if (selectedTypes.length === 0) return assets;
  const aliasSet = new Set<string>();
  for (const value of selectedTypes) {
    const opt = options.find((o) => o.value === value);
    if (opt) opt.aliases.forEach((a) => aliasSet.add(a.toLowerCase()));
    else aliasSet.add(value.toLowerCase());
  }
  return assets.filter((a) => aliasSet.has((a.Type || "").trim().toLowerCase()));
}

export interface AssetTypeGroup {
  category: AssetTypeCategory;
  options: AssetTypeOption[];
}

const CATEGORY_ORDER: AssetTypeCategory[] = ["servers", "databases", "middleware", "extension"];

/** 按固定分类顺序分组，丢弃空组（保持各组内 options 原顺序）。 */
export function buildAssetTypeGroups(options: AssetTypeOption[]): AssetTypeGroup[] {
  return CATEGORY_ORDER.map((category) => ({
    category,
    options: options.filter((o) => o.category === category),
  })).filter((g) => g.options.length > 0);
}

/** 按解析后的显示名或 value 子串过滤（大小写不敏感）；空查询返回全部。 */
export function filterAssetTypeOptions(
  options: AssetTypeOption[],
  query: string,
  resolveLabel: (o: AssetTypeOption) => string
): AssetTypeOption[] {
  const q = query.trim().toLowerCase();
  if (!q) return options;
  return options.filter((o) => resolveLabel(o).toLowerCase().includes(q) || o.value.toLowerCase().includes(q));
}

/** 解析选项展示标签：内置走默认命名空间，扩展走其 `i18nNs`（ext-<name>）命名空间。 */
export function resolveAssetTypeLabel(option: AssetTypeOption, t: TranslateFn): string {
  if (!option.labelIsI18nKey) return option.label;
  return t(option.label, option.i18nNs ? { ns: option.i18nNs } : undefined);
}

/** 取某类型的展示标签；未命中返回原始 type（兼容未知/未加载扩展）。 */
export function getAssetTypeLabel(type: string, t: TranslateFn, options: AssetTypeOption[]): string {
  const opt = options.find((o) => o.value === type);
  if (!opt) return type;
  return resolveAssetTypeLabel(opt, t);
}
