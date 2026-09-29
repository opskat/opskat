import { Boxes } from "lucide-react";
import { registerAssetType } from "./_register";
import { GenericConfigSection } from "@/components/asset/GenericConfigSection";
import { parseGenericConfig } from "@/components/asset/GenericConfigSection.config";
import { GenericDetailInfoCard, GenericDetailSubtitle } from "@/components/asset/detail/GenericDetailInfoCard";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import type { asset_entity } from "../../../wailsjs/go/models";

/** 通用资产引用的自定义类型标识。 */
function customTypeSlugOf(asset: asset_entity.Asset): string | undefined {
  return parseGenericConfig(asset.Config).custom_type || undefined;
}

/** 「恢复默认」= 复制所属自定义类型当前的默认策略(与后端新建资产时的做法一致)。 */
async function loadCustomTypeDefaultPolicy(asset: asset_entity.Asset): Promise<string> {
  const store = useCustomTypeStore.getState();
  if (!store.loaded) await store.load();
  const slug = customTypeSlugOf(asset);
  const summary = useCustomTypeStore.getState().types.find((ct) => ct.slug === slug);
  if (!summary) throw new Error(`custom type not found: ${slug}`);
  const ct = await store.get(summary.id);
  return JSON.stringify(ct.defaultPolicy ?? {});
}

// 通用资产:配置与执行方式来自自定义类型,资产只存字段值。选择器里「自定义」分组按
// 每个自定义类型展开(options.getAssetTypeOptions 的 customTypes),不单列本定义。
registerAssetType({
  type: "generic",
  icon: Boxes,
  aliases: ["generic"],
  label: "assetType.generic",
  category: "custom",
  canConnect: false,
  canConnectInNewTab: false,
  connectAction: "page",
  DetailInfoCard: GenericDetailInfoCard,
  DetailSubtitle: GenericDetailSubtitle,
  ConfigSection: GenericConfigSection,
  testable: true,
  variantOf: customTypeSlugOf,
  policy: {
    policyType: "generic",
    titleKey: "asset.generic.policyTitle",
    hintKey: "asset.generic.policyHint",
    testPlaceholderKey: "asset.generic.policyPlaceholder",
    testable: false,
    loadDefault: loadCustomTypeDefaultPolicy,
    fields: [
      {
        key: "allow_list",
        labelKey: "asset.cmdPolicyAllowList",
        placeholderKey: "asset.generic.policyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_list",
        labelKey: "asset.cmdPolicyDenyList",
        placeholderKey: "asset.generic.policyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
