import type { ComponentType } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";
import type { ConfigSectionComponent } from "./formContract";

export interface DetailInfoCardProps {
  asset: asset_entity.Asset;
  sshTunnelName: (id?: number) => string | null;
}

export interface PolicyFieldDef {
  key: string;
  labelKey: string;
  placeholderKey: string;
  variant: "allow" | "deny" | "warn";
}

export interface PolicyDefinition {
  policyType: string;
  titleKey: string;
  hintKey: string;
  testPlaceholderKey: string;
  fields: PolicyFieldDef[];
}

/** 语义分组（资产类型选择器展示用）。 */
export type AssetTypeCategory = "servers" | "databases" | "middleware" | "extension";

export interface AssetTypeDefinition {
  type: string;
  icon: ComponentType<{ className?: string; style?: React.CSSProperties }>;
  /** 所有应匹配此类型的 `asset.Type` 值（含历史别名）。 */
  aliases: string[];
  /** 选择器展示标签的 i18n key（默认命名空间），如 `nav.ssh`。 */
  label: string;
  /** 选择器语义分组。 */
  category: AssetTypeCategory;
  canConnect: boolean;
  canConnectInNewTab: boolean;
  connectAction: "terminal" | "query";
  /** 是否在右键菜单暴露 SFTP 文件管理动作(替代 AssetTree 的 `asset.Type === "ssh"` 特例);缺省 = 不暴露。 */
  canOpenFileManager?: boolean;
  DetailInfoCard: ComponentType<DetailInfoCardProps>;
  /** 资产表单的 per-type config 区(注册化表单);缺省 = 走遗留/扩展路径。 */
  ConfigSection?: ConfigSectionComponent;
  /** 是否支持"测试连接"(替代 isTestableAssetType 链)。 */
  testable?: boolean;
  policy?: PolicyDefinition;
}
