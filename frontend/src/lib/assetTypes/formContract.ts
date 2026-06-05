import type { ForwardRefExoticComponent, RefAttributes } from "react";
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

/** 每个 ConfigSection 经 useImperativeHandle 暴露的命令式句柄。 */
export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 仅可测类型实现;不可测类型为 null。 */
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null;
}

export interface SectionValidity {
  canTest: boolean;
  canSave: boolean;
  /** 保存禁用原因的 i18n key;空/缺省 = 可保存(壳据此显示提示)。 */
  saveDisabledReason?: string;
}

export interface ConfigSectionProps {
  /** 编辑态回填来源;创建态为 undefined。 */
  editAsset?: asset_entity.Asset;
  ctx: AssetFormContext;
  /** state 变化时上报,驱动壳 Test/Save 按钮启用态(反应式)。 */
  onValidityChange: (v: SectionValidity) => void;
  /** 仅 database 用:driver 变化时驱动壳 icon(其它 section 忽略)。 */
  onIconChange?: (icon: string) => void;
}

export type ConfigSectionComponent = ForwardRefExoticComponent<ConfigSectionProps & RefAttributes<AssetFormHandle>>;
