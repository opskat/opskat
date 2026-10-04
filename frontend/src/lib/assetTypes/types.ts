import type { ComponentType } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";
import type { ConfigSectionComponent } from "./formContract";

export interface DetailInfoCardProps {
  asset: asset_entity.Asset;
  sshTunnelName: (id?: number) => string | null;
  /** 打开这台资产的编辑表单(如通用资产缺值横幅的「去填写」)。 */
  onEdit?: () => void;
}

export interface DetailSubtitleProps {
  asset: asset_entity.Asset;
}

export interface PolicyFieldDef {
  key: string;
  labelKey: string;
  /** 占位符的 i18n key。与 `placeholder` 二选一，不是兜底关系。 */
  placeholderKey?: string;
  /** 字面占位符。扩展类型用它列出 manifest 声明的 action 名——那是数据，不可翻译。 */
  placeholder?: string;
  variant: "allow" | "deny" | "warn";
}

export interface PolicyDefinition {
  policyType: string;
  titleKey: string;
  /** titleKey / hintKey 的 i18next 命名空间；扩展的文案住在 `ext-<name>` 里。 */
  ns?: string;
  /** 规则语法提示；扩展的规则就是 action 名，没有额外语法可讲，故可缺省。 */
  hintKey?: string;
  /** 规则测试框的占位符；只有支持规则测试的类型提供。 */
  testPlaceholderKey?: string;
  fields: PolicyFieldDef[];
  /** 策略卡是否提供「规则测试」;缺省 = 提供(需后端 TestPolicyRule 支持该 policyType)。 */
  testable?: boolean;
  /** 「恢复默认」取的默认策略 JSON;缺省 = 后端 GetDefaultPolicy(asset.Type)。 */
  loadDefault?: (asset: asset_entity.Asset) => Promise<string>;
}

/** 语义分组（资产类型选择器展示用）。 */
export type AssetTypeCategory = "servers" | "databases" | "middleware" | "custom" | "extension";

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
  connectAction: "terminal" | "query" | "page";
  pageId?: string;
  /** page 类型的 tab id 前缀；缺省取 pageId。见 assetTypes.pageTabPrefix。 */
  pageTabPrefix?: string;
  pageIcon?: string;
  /** 提供该类型的扩展名；内置类型缺省。page tab 的 meta 靠它路由到扩展页面。 */
  extensionName?: string;
  /** 选择器标签 `label` 的 i18next 命名空间；扩展的文案住在 `ext-<name>` 里。 */
  labelNs?: string;
  /** 是否在右键菜单暴露 SFTP 文件管理动作(替代 AssetTree 的 `asset.Type === "ssh"` 特例);缺省 = 不暴露。 */
  canOpenFileManager?: boolean;
  DetailInfoCard: ComponentType<DetailInfoCardProps>;
  /** 详情页头部资产名下方的副标题;缺省 = 大写的 asset.Type。 */
  DetailSubtitle?: ComponentType<DetailSubtitleProps>;
  /** 从已存资产读出类型选择器里的子类型(通用资产 = 自定义类型标识);与 ConfigSectionProps.variant 对应。 */
  variantOf?: (asset: asset_entity.Asset) => string | undefined;
  /**
   * 资产表单给这类资产预填的图标名;缺省 = 表单按类型名取内置默认图标。空串 = 不预填(通用资产:
   * 图标来自所选自定义类型,类型没有图标就留空,显示时由 getIconComponent 取默认图标)。
   */
  formIcon?: string;
  /** 资产表单的 per-type config 区(注册化表单);缺省 = 走遗留/扩展路径。 */
  ConfigSection?: ConfigSectionComponent;
  /** 是否支持"测试连接"(替代 isTestableAssetType 链)。 */
  testable?: boolean;
  policy?: PolicyDefinition;
}
