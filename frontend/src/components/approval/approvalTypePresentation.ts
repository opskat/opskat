import type { ComponentType } from "react";
import { Globe, KeyRound, SquareTerminal } from "lucide-react";

/** 审批项徽标上的操作:图标 + 文案 key。 */
export interface ApprovalOp {
  icon: ComponentType<{ className?: string }>;
  labelKey: string;
}

/**
 * 一种审批类型(后端 permission.ApprovalTypeFor)在审批界面上与默认呈现的差异。审批组件按类型查
 * 这张表,自己不认识具体类型;没登记的类型全部走默认呈现(类型名大写的徽标、detail 可折叠)。
 */
export interface ApprovalTypePresentation {
  /** 徽标显示操作类型而不是类型名。 */
  op?: (command: string) => ApprovalOp;
  /**
   * detail 是"这一条自己"的说明(目标地址 / 用什么执行),批准时必须看到:逐条常驻展示,这一批
   * 也不能按"每条共享同一句摘要"折叠——折叠是给 cp 那种整批一句摘要的批用的。值是它的标题文案 key。
   */
  pinnedDetailLabelKey?: string;
}

const presentations: Record<string, ApprovalTypePresentation> = {
  // 通用资产(permission.ApprovalTypeFor("generic"))。
  generic: { op: genericApprovalOp, pinnedDetailLabelKey: "ai.approvalGenericTarget" },
};

const defaultPresentation: ApprovalTypePresentation = {};

export function approvalTypePresentation(type: string): ApprovalTypePresentation {
  return presentations[type] ?? defaultPresentation;
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
