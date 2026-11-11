import type { ComponentType } from "react";
import {
  Boxes,
  Database,
  FileEdit,
  FilePlus,
  FileUp,
  Globe,
  KeyRound,
  Puzzle,
  Server,
  SquareTerminal,
  Terminal,
  Trash2,
  Usb,
} from "lucide-react";
import { S3Icon } from "@/components/asset/brand-icons";

// lucide 图标是 ForwardRefExoticComponent，S3Icon 是普通的 React.FC：按徽标唯一用到的形状
// （接收 className 的组件）收窄类型。
type IconComponent = ComponentType<{ className?: string }>;

/** 审批项徽标上的操作:图标 + 文案 key。 */
export interface ApprovalOp {
  icon: IconComponent;
  labelKey: string;
}

/**
 * 一种审批类型(后端 permission.ApprovalTypeFor)在审批界面上与默认呈现的差异。审批组件按类型查
 * 这张表,自己不认识具体类型;没登记的类型全部走默认呈现(原始类型名的徽标、detail 可折叠)。
 */
export interface ApprovalTypePresentation {
  /** 徽标上的类型名(翻译 key);没登记的类型(扩展的动态资产类型)显示原始类型名。 */
  labelKey?: string;
  /** 徽标图标;缺省为终端图标。 */
  icon?: IconComponent;
  /** 徽标显示操作类型而不是类型名。 */
  op?: (command: string) => ApprovalOp;
  /**
   * detail 是"这一条自己"的说明(目标地址 / 用什么执行),批准时必须看到:逐条常驻展示,这一批
   * 也不能按"每条共享同一句摘要"折叠——折叠是给 cp 那种整批一句摘要的批用的。值是它的标题文案 key。
   */
  pinnedDetailLabelKey?: string;
}

const typeIcons: Record<string, IconComponent> = {
  exec: Terminal,
  serial: Usb,
  sql: Database,
  redis: Server,
  mongo: Database,
  kafka: Database,
  grant: Globe,
  cp: FileUp,
  local_bash: Terminal,
  local_write: FilePlus,
  local_edit: FileEdit,
  delete: Trash2,
  etcd: Database,
  k8s: Boxes,
  oss: S3Icon,
};

const presentations: Record<string, ApprovalTypePresentation> = {
  ...Object.fromEntries(
    Object.entries(typeIcons).map(([type, icon]) => [type, { labelKey: `ai.approvalType.${type}`, icon }])
  ),
  // 通用资产(permission.ApprovalTypeFor("generic"))。
  generic: { op: genericApprovalOp, pinnedDetailLabelKey: "ai.approvalGenericTarget" },
  ext_dev_install: { icon: Puzzle },
};

const defaultPresentation: ApprovalTypePresentation = {};

export function approvalTypePresentation(type: string): ApprovalTypePresentation {
  return presentations[type] ?? defaultPresentation;
}

/**
 * 审批项徽标的图标与文案 key:登记了操作类型的(通用资产)显示操作,其余显示类型名;
 * labelKey 缺省时调用方显示原始类型名。AI 审批块与 opsctl 审批框共用,两处不各自维护一张表。
 */
export function approvalBadge(type: string, command: string): { icon: IconComponent; labelKey?: string } {
  const presentation = approvalTypePresentation(type);
  const op = presentation.op?.(command);
  return { icon: op?.icon ?? presentation.icon ?? Terminal, labelKey: op?.labelKey ?? presentation.labelKey };
}

/** 这一条的 detail 是否要逐条常驻展示(见 ApprovalTypePresentation.pinnedDetailLabelKey)。 */
export function hasPinnedDetail<T extends { type: string; detail?: string }>(item: T): item is T & { detail: string } {
  return !!item.detail && !!approvalTypePresentation(item.type).pinnedDetailLabelKey;
}

/**
 * 按匹配对象的形状区分通用资产的三种操作:取值 `secret:<字段名>`(字段名是标识符)、HTTP
 * `<METHOD> /<path>`、其余为本地命令(后端 generic_policy.go 的匹配对象约定)。只是以
 * "secret:" 开头的命令(如 `secret:x; rm -rf /`)仍是命令。
 */
function genericApprovalOp(subject: string): ApprovalOp {
  if (/^secret:[A-Za-z_][A-Za-z0-9_]*$/.test(subject))
    return { icon: KeyRound, labelKey: "opsctlApproval.genericSecret" };
  if (/^[A-Z]+ \//.test(subject)) return { icon: Globe, labelKey: "opsctlApproval.genericHttp" };
  return { icon: SquareTerminal, labelKey: "opsctlApproval.genericCommand" };
}
