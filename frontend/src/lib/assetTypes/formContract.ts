import type { ComponentType, Ref } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";

/** 父壳交给每个 section 的共享横切助手；按类型需要逐步扩充(YAGNI)。 */
export interface AssetFormContext {
  isEdit: boolean;
  /** 明文→密文(走后端 EncryptPassword);凭据类型用,local 不用。 */
  encryptPassword: (plain: string) => Promise<string>;
}

/** 保存序列化结果。 */
export interface AssetConfigBuildResult {
  configJSON: string;
  /** ssh_asset_id 关联;无隧道类型恒 0。 */
  sshTunnelId: number;
}

/** 测试连接所需最小信息(壳据此调 TestAssetConnection);serial 等无密码传 ""。 */
export interface AssetTestConfig {
  assetType: string;
  configJSON: string;
  password: string;
}

/** 测试成功的可选实际结果摘要；父壳负责统一成功 toast。 */
export interface AssetTestResult {
  successDetail?: string;
}

/** section 可选提供的自定义测试生命周期；cancel 必须幂等且同步启动清理。 */
export interface AssetTestAttempt {
  result: Promise<AssetTestResult>;
  cancel: () => void;
  /** 协议可把 typed failure 映射为与正常会话一致的用户文案；undefined 走壳的通用错误。 */
  errorMessage?: (error: unknown) => string | undefined;
}

/** 每个 ConfigSection 经 useImperativeHandle 暴露的命令式句柄。 */
export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 仅可测类型实现;不可测类型为 null。 */
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null;
  /** 协议需要前后端协同测试时提供；父壳不识别具体资产类型。 */
  startTest?: (ctx: AssetFormContext) => AssetTestAttempt;
}

export interface SectionValidity {
  canTest: boolean;
  canSave: boolean;
  /** 保存禁用原因的 i18n key;空/缺省 = 可保存(壳据此显示提示)。 */
  saveDisabledReason?: string;
}

export interface ConfigSectionProps {
  /** 壳经此拿 AssetFormHandle(React 19:ref 即普通 prop)。 */
  ref?: Ref<AssetFormHandle>;
  /** 编辑态回填来源;创建态为 undefined。 */
  editAsset?: asset_entity.Asset;
  ctx: AssetFormContext;
  /** state 变化时上报,驱动壳 Test/Save 按钮启用态(反应式)。 */
  onValidityChange: (v: SectionValidity) => void;
  /** 仅 database 用:driver 变化时驱动壳 icon(其它 section 忽略)。 */
  onIconChange?: (icon: string) => void;
}

export type ConfigSectionComponent = ComponentType<ConfigSectionProps>;
