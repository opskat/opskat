import type { TFunction } from "i18next";

// 决策来源标签：id 对应 audit.decisionSources.<id> 的译文，样式按放行/拒绝区分
const DECISION_SOURCES: Record<string, { id: string; className: string }> = {
  policy_allow: { id: "policy", className: "bg-success/15 text-success" },
  policy_deny: { id: "policy", className: "bg-destructive/15 text-destructive" },
  user_allow: { id: "user", className: "bg-success/15 text-success" },
  user_deny: { id: "user", className: "bg-destructive/15 text-destructive" },
  grant_allow: { id: "grant", className: "bg-info/15 text-info" },
  grant_deny: { id: "grant", className: "bg-destructive/15 text-destructive" },
  assisted_allow: { id: "assisted", className: "bg-success/15 text-success" },
  autopilot_allow: { id: "autopilot", className: "bg-success/15 text-success" },
  autopilot_deny: { id: "autopilot", className: "bg-destructive/15 text-destructive" },
  exec_unsupported_type: { id: "execUnsupportedType", className: "bg-destructive/15 text-destructive" },
  exec_gate_blocked: { id: "execGateBlocked", className: "bg-destructive/15 text-destructive" },
  exec_precheck_failed: { id: "execPrecheckFailed", className: "bg-destructive/15 text-destructive" },
  exec_canonicalize_error: { id: "execCanonicalizeError", className: "bg-destructive/15 text-destructive" },
  exec_type_mismatch: { id: "execTypeMismatch", className: "bg-destructive/15 text-destructive" },
  exec_resolve_failed: { id: "execResolveFailed", className: "bg-destructive/15 text-destructive" },
  exec_invalid_input: { id: "execInvalidInput", className: "bg-destructive/15 text-destructive" },
  batch_complete: { id: "batchComplete", className: "bg-success/15 text-success" },
  batch_partial_failure: { id: "batchPartialFailure", className: "bg-destructive/15 text-destructive" },
  batch_failed: { id: "batchFailed", className: "bg-destructive/15 text-destructive" },
};

export function decisionSourceBadge(source: string, t: TFunction): { label: string; className: string } {
  const known = DECISION_SOURCES[source];
  if (!known) return { label: source || "-", className: "bg-muted" };
  return { label: t(`audit.decisionSources.${known.id}`), className: known.className };
}

// 决策（allow/deny）的译文；未知值原样显示
export function decisionLabel(decision: string, t: TFunction): string {
  return decision === "allow" || decision === "deny" ? t(`audit.decisions.${decision}`) : decision || "-";
}

// 来源（ai/opsctl/desktop）的译文；未知值原样显示
export const AUDIT_SOURCES = ["opsctl", "ai", "desktop"] as const;
export function sourceLabel(source: string, t: TFunction): string {
  return (AUDIT_SOURCES as readonly string[]).includes(source) ? t(`audit.sources.${source}`) : source || "-";
}

// 决策方分组的译文（后端分组 id 即 audit.deciders 下的键）
export function deciderLabel(group: string, t: TFunction): string {
  return t(`audit.deciders.${group}`);
}
