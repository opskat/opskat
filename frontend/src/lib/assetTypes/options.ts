// frontend/src/lib/assetTypes/options.ts
import type { ComponentType } from "react";
import { Server } from "lucide-react";
import { getIconComponent } from "@/components/asset/IconPicker";
import { getBuiltinTypes } from "./index";
import type { AssetTypeCategory } from "./types";
import type { ExtManifest } from "@/extension/types";
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

interface ExtensionEntryLike {
  manifest: ExtManifest;
}

/** 内置资产类型选项：从 registry 的 AssetTypeDefinition 派生（单一来源）。 */
function builtinOptions(): AssetTypeOption[] {
  return getBuiltinTypes().map((def) => ({
    value: def.type,
    aliases: def.aliases,
    label: def.label,
    labelIsI18nKey: true,
    icon: def.icon,
    group: "builtin",
    category: def.category,
  }));
}

export function getAssetTypeOptions(extensions: Record<string, ExtensionEntryLike>): AssetTypeOption[] {
  const out: AssetTypeOption[] = builtinOptions();
  for (const entry of Object.values(extensions)) {
    const m = entry.manifest;
    if (!m.assetTypes?.length) continue;
    for (const at of m.assetTypes) {
      out.push({
        value: at.type,
        aliases: [at.type],
        label: at.i18n?.name ?? at.type,
        labelIsI18nKey: true,
        i18nNs: `ext-${m.name}`,
        icon: m.icon ? getIconComponent(m.icon) : Server,
        group: "extension",
        category: "extension",
      });
    }
  }
  return out;
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
