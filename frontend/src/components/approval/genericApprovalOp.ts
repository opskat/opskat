import type { ComponentType } from "react";
import { Globe, KeyRound, SquareTerminal } from "lucide-react";

/** 通用资产审批项的类型标签(后端 permission.ApprovalTypeFor("generic"))。 */
export const GENERIC_APPROVAL_TYPE = "generic";

export interface GenericApprovalOp {
  icon: ComponentType<{ className?: string }>;
  labelKey: string;
}

/**
 * 按匹配对象的形状区分通用资产的三种操作:取值 `secret:<字段名>`(字段名是标识符)、HTTP
 * `<METHOD> /<path>`、其余为本地命令(后端 generic_policy.go 的匹配对象约定)。只是以
 * "secret:" 开头的命令(如 `secret:x; rm -rf /`)仍是命令。
 */
export function genericApprovalOp(subject: string): GenericApprovalOp {
  if (/^secret:[A-Za-z_][A-Za-z0-9_]*$/.test(subject))
    return { icon: KeyRound, labelKey: "opsctlApproval.genericSecret" };
  if (/^[A-Z]+ \//.test(subject)) return { icon: Globe, labelKey: "opsctlApproval.genericHttp" };
  return { icon: SquareTerminal, labelKey: "opsctlApproval.genericCommand" };
}
