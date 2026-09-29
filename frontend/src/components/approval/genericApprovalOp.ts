import type { ComponentType } from "react";
import { Globe, KeyRound, SquareTerminal } from "lucide-react";

/** 通用资产审批项的类型标签(后端 permission.ApprovalTypeFor("generic"))。 */
export const GENERIC_APPROVAL_TYPE = "generic";

export interface GenericApprovalOp {
  icon: ComponentType<{ className?: string }>;
  labelKey: string;
}

/**
 * 按匹配对象的形状区分通用资产的三种操作:取值 `secret:<字段>`、HTTP `<METHOD> /<path>`、
 * 其余为本地命令(后端 generic_policy.go 的匹配对象约定)。
 */
export function genericApprovalOp(subject: string): GenericApprovalOp {
  if (subject.startsWith("secret:")) return { icon: KeyRound, labelKey: "opsctlApproval.genericSecret" };
  if (/^[A-Z]+ \//.test(subject)) return { icon: Globe, labelKey: "opsctlApproval.genericHttp" };
  return { icon: SquareTerminal, labelKey: "opsctlApproval.genericCommand" };
}
