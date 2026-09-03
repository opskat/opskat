// frontend/src/extension/assetTypes.ts
import { Server } from "lucide-react";
import { getIconComponent } from "@/components/asset/IconPicker";
import { registerAssetType, unregisterAssetType } from "@/lib/assetTypes";
import { makeExtensionConfigSection } from "@/components/asset/ExtensionConfigSection";
import { makeExtensionDetailInfoCard } from "@/components/asset/detail/ExtensionDetailInfoCard";
import type { AssetTypeDefinition, PolicyDefinition } from "@/lib/assetTypes/types";
import type { ExtensionConfigSchema } from "./configSchema";
import type { ExtManifest } from "./types";

const registeredTypes = new Map<string, string[]>(); // extension name → asset types

/**
 * 把一个扩展 manifest 声明的资产类型注册进**内置类型用的同一张**注册表。
 *
 * 这是前端这一半的关键：注册之后，类型选择器、资产树筛选、表单、详情页、连接派发全部
 * 从注册表读同一份定义，没有一处需要再问"这个类型是不是扩展提供的"。
 */
export function registerExtensionAssetTypes(name: string, manifest: ExtManifest): void {
  unregisterExtensionAssetTypes(name);
  const types: string[] = [];
  for (const at of manifest.assetTypes ?? []) {
    registerAssetType(buildDefinition(name, manifest, at.type, at.i18n?.name, at.configSchema));
    types.push(at.type);
  }
  if (types.length > 0) registeredTypes.set(name, types);
}

export function unregisterExtensionAssetTypes(name: string): void {
  for (const type of registeredTypes.get(name) ?? []) {
    unregisterAssetType(type);
  }
  registeredTypes.delete(name);
}

function buildDefinition(
  extensionName: string,
  manifest: ExtManifest,
  type: string,
  labelKey: string | undefined,
  rawSchema: Record<string, unknown> | undefined
): AssetTypeDefinition {
  const ns = `ext-${extensionName}`;
  const schema = rawSchema as ExtensionConfigSchema | undefined;
  // slot="asset.connect" 是 manifest 里声明"双击这个资产打开哪个页面"的方式。
  const connectPage = manifest.frontend?.pages.find((p) => p.slot === "asset.connect");

  return {
    type,
    icon: manifest.icon ? getIconComponent(manifest.icon) : Server,
    aliases: [type],
    label: labelKey ?? type,
    labelNs: ns,
    category: "extension",
    canConnect: !!connectPage,
    // 新标签打开走的是终端连接路径，扩展页面没有这个概念。
    canConnectInNewTab: false,
    connectAction: "page",
    pageId: connectPage?.id,
    // tab id 里带扩展名与页面 id，避免两个扩展的同名页面互相顶掉对方的 tab。
    pageTabPrefix: connectPage ? `ext-${extensionName}-${connectPage.id}` : undefined,
    pageIcon: manifest.icon,
    extensionName,
    DetailInfoCard: makeExtensionDetailInfoCard({
      displayNameKey: manifest.i18n.displayName,
      ns,
      assetType: type,
      schema,
    }),
    ConfigSection: makeExtensionConfigSection({
      extensionName,
      assetType: type,
      schema,
      hasBackend: !!manifest.backend,
    }),
    // 扩展资产的连通性由扩展自己的 action 验证（ExtensionConfigForm 里的测试按钮），
    // 不走宿主的 TestAssetConnection。
    testable: false,
    policy: buildPolicy(manifest, ns),
  };
}

function buildPolicy(manifest: ExtManifest, ns: string): PolicyDefinition | undefined {
  const policies = manifest.policies;
  if (!policies?.type) return undefined;
  // 扩展的规则语言是 action 名，可用取值由 manifest 列出——那是数据不是文案，
  // 所以作为字面占位符给出，而标题走扩展自己的 i18n 命名空间。
  const placeholder = (policies.actions ?? []).join(", ");
  return {
    policyType: policies.type,
    titleKey: manifest.i18n.displayName,
    ns,
    fields: [
      { key: "allow_list", labelKey: "asset.cmdPolicyAllowList", placeholder, variant: "allow" },
      { key: "deny_list", labelKey: "asset.cmdPolicyDenyList", placeholder, variant: "deny" },
    ],
  };
}
