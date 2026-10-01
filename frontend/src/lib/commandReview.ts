import type { TFunction } from "i18next";

// 命令的模型审核（辅助审批 / Autopilot）在前端用到的类型与纯函数。
// 取值与后端 internal/model/entity/policy/permission_mode.go、internal/ai/aictx/review.go 一致。

/** 资产 / 分组上的权限模式；空字符串表示沿用上级分组。 */
export type PermissionMode = "" | "default" | "assisted" | "autopilot";
/** 生效的权限模式（沿用已经解析过）。 */
export type EffectivePermissionMode = Exclude<PermissionMode, "">;

/** 一次模型审核的结果，对应后端 aictx.ReviewInfo 的 JSON。 */
export interface ReviewInfo {
  mode: "assisted" | "autopilot"; // 触发审核的权限模式
  outcome: string; // pass / reject / fail
  reason?: string; // 审核失败的原因
  failed?: string[]; // 审核未通过时没满足条件的题目
  model?: string;
  scores?: Record<string, number>; // 每道题回答"是"的概率
  threshold?: number; // 判断用的阈值；和 scores 一起出现
  duration_ms?: number;
  cached?: boolean;
  attempts?: number; // 调用模型的次数，超时或连接出错时会重试
}

/** 审核题目，顺序即界面上的显示顺序。与后端 command_review_svc 的题目 ID 一致。 */
export const REVIEW_QUESTIONS: readonly string[] = ["destructive", "disruptive", "remote_code"];
const REVIEW_FAIL_REASONS: readonly string[] = [
  "not_configured",
  "api_key_unreadable",
  "invalid_api_key",
  "timeout",
  "too_long",
  "unparseable",
  "undecodable",
  "unavailable",
];

/** 把审核结果写成一句话，例如"模型审核未通过（可能中断服务）"。 */
export function reviewSummary(t: TFunction, review: ReviewInfo): string {
  if (review.outcome === "pass") return t("commandReview.summary.pass");
  if (review.outcome === "reject") {
    const detail = (review.failed ?? [])
      .map((q) => (REVIEW_QUESTIONS.includes(q) ? t(`commandReview.question.${q}`) : q))
      .join(t("commandReview.listSeparator"));
    return t("commandReview.summary.reject", { detail });
  }
  return t("commandReview.summary.fail", { detail: failReasonLabel(t, review.reason) });
}

/** 在审核结果前写上触发审核的权限模式，例如"Autopilot · 模型审核通过"，审计里用来区分是哪种模式审的。 */
export function reviewWithMode(t: TFunction, review: ReviewInfo): string {
  return t("commandReview.withMode", {
    mode: t(`commandReview.mode.${review.mode}`),
    summary: reviewSummary(t, review),
  });
}

/** 解析审计记录里存的审核结果 JSON；不是合法的审核结果时返回 null，由调用方原样显示。 */
export function parseReviewInfo(raw: string): ReviewInfo | null {
  try {
    const v: unknown = JSON.parse(raw);
    if (v && typeof v === "object" && typeof (v as ReviewInfo).outcome === "string") return v as ReviewInfo;
  } catch {
    // 不是合法 JSON：交给调用方原样显示
  }
  return null;
}

/** 审核失败原因的文案；未知原因按服务不可用显示。 */
export function failReasonLabel(t: TFunction, reason: string | undefined): string {
  const known = reason && REVIEW_FAIL_REASONS.includes(reason) ? reason : "unavailable";
  return t(`commandReview.reason.${known}`);
}

// 与后端 policy.ResolveGroupChain 一致：最多向上找 5 层。
const MAX_GROUP_DEPTH = 5;

interface GroupLike {
  ID: number;
  ParentID: number;
  Name: string;
  permissionMode?: string;
}

/** 沿用得到的权限模式，以及它来自哪个分组；没有分组设置时 from 为 null，按默认处理。 */
export interface InheritedPermissionMode<G extends GroupLike = GroupLike> {
  mode: EffectivePermissionMode;
  from: G | null;
}

/** 从 groupId 起沿分组链向上找第一个设置了权限模式的分组；都没有（或分组不存在）就是默认。 */
export function resolveInheritedPermissionMode<G extends GroupLike>(
  groupId: number,
  groups: readonly G[]
): InheritedPermissionMode<G> {
  const byId = new Map(groups.map((g) => [g.ID, g]));
  let id = groupId;
  for (let depth = 0; depth < MAX_GROUP_DEPTH && id > 0; depth++) {
    const g = byId.get(id);
    if (!g) break;
    if (g.permissionMode) return { mode: g.permissionMode as EffectivePermissionMode, from: g };
    id = g.ParentID;
  }
  return { mode: "default", from: null };
}
