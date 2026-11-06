// frontend/src/lib/assetTypes/options.ts
import { useMemo, type ComponentType } from "react";
import { getAllAssetTypes, useAssetTypes } from "./index";
import type { AssetTypeCategory, AssetTypeDefinition } from "./types";
import type { asset_entity } from "../../../wailsjs/go/models";
import { getIconComponent } from "@/components/asset/IconPicker";

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
  group: "builtin" | "extension" | "custom";
  /** 语义分组（选择器展示用）。 */
  category: AssetTypeCategory;
  /** 子类型：同一 `value` 下区分选项（通用资产 = 自定义类型标识）。 */
  variant?: string;
  /** 选中后给资产预填的图标（自定义类型的图标）；缺省由表单按类型取默认图标。 */
  defaultIcon?: string;
}

/** 自定义类型列表项（customtype.Summary 的子集）。 */
export interface CustomTypeEntryLike {
  slug: string;
  name: string;
  icon: string;
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

/** 每个自定义类型一项：挂在「自定义」分组的定义（通用资产）下，variant = 类型标识。 */
function customTypeOptions(def: AssetTypeDefinition, customTypes: CustomTypeEntryLike[]): AssetTypeOption[] {
  return customTypes.map((ct) => ({
    value: def.type,
    aliases: def.aliases,
    label: ct.name,
    labelIsI18nKey: false,
    icon: getIconComponent(ct.icon),
    group: "custom" as const,
    category: def.category,
    variant: ct.slug,
    defaultIcon: ct.icon || undefined,
  }));
}

function toOptions(defs: AssetTypeDefinition[], customTypes?: CustomTypeEntryLike[]): AssetTypeOption[] {
  if (!customTypes) return defs.map(toOption);
  return defs.flatMap((def) => (def.category === "custom" ? customTypeOptions(def, customTypes) : [toOption(def)]));
}

/**
 * 全部资产类型选项，从注册表派生（单一来源；扩展类型也在注册表里）。给出 customTypes（新建资产
 * 的类型选择器）时，「自定义」分组的定义换成每个自定义类型一项；不给时（资产树类型筛选）保留
 * 一项，按 asset.Type 匹配全部通用资产。
 */
export function getAssetTypeOptions(customTypes?: CustomTypeEntryLike[]): AssetTypeOption[] {
  return toOptions(getAllAssetTypes(), customTypes);
}

/** 响应式版本：注册表增删（扩展启用/禁用）或自定义类型列表变化时组件会重渲染。
 *  返回值在输入不变时保持同一引用——下游 useMemo 会拿它做依赖。 */
export function useAssetTypeOptions(customTypes?: CustomTypeEntryLike[]): AssetTypeOption[] {
  const defs = useAssetTypes();
  return useMemo(() => toOptions(defs, customTypes), [defs, customTypes]);
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

const CATEGORY_ORDER: AssetTypeCategory[] = ["servers", "databases", "middleware", "extension", "custom"];

/** 选项的唯一键（React key / data-testid）：带子类型时为 `value:variant`。 */
export function optionKey(o: AssetTypeOption): string {
  return o.variant ? `${o.value}:${o.variant}` : o.value;
}

/** 按 value + 子类型查选项；子类型选项查不到（如列表未加载）时退到同 value 的无子类型选项。 */
export function findAssetTypeOption(
  options: AssetTypeOption[],
  value: string,
  variant?: string
): AssetTypeOption | undefined {
  return (
    options.find((o) => o.value === value && o.variant === variant) ??
    options.find((o) => o.value === value && !o.variant)
  );
}

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
export function getAssetTypeLabel(type: string, t: TranslateFn, options: AssetTypeOption[], variant?: string): string {
  const opt = findAssetTypeOption(options, type, variant);
  if (!opt) return type;
  return resolveAssetTypeLabel(opt, t);
}
