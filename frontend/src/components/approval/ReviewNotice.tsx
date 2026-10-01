import { useTranslation } from "react-i18next";
import { cn } from "@opskat/ui";
import { ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";
import { REVIEW_QUESTIONS, reviewSummary, reviewWithMode, type ReviewInfo } from "@/lib/commandReview";

function reviewIcon(outcome: string) {
  if (outcome === "pass") return { Icon: ShieldCheck, tone: "text-success" };
  if (outcome === "reject") return { Icon: ShieldAlert, tone: "text-warning" };
  return { Icon: ShieldQuestion, tone: "text-muted-foreground" };
}

/**
 * 显示一次模型审核的结果（审批项、审计详情共用），例如"模型审核未通过（可能中断服务）"。
 * withMode 时在前面写上触发审核的权限模式（审计里用；审批时只会是辅助审批，不需要写）。
 */
export function ReviewNotice({
  review,
  withMode,
  className,
}: {
  review: ReviewInfo;
  withMode?: boolean;
  className?: string;
}) {
  const { t } = useTranslation();
  const { Icon, tone } = reviewIcon(review.outcome);
  return (
    <div data-testid="review-notice" className={cn("flex items-start gap-1.5 text-xs", className)}>
      <Icon aria-hidden className={cn("mt-0.5 h-3.5 w-3.5 shrink-0", tone)} />
      <span className="text-foreground">{withMode ? reviewWithMode(t, review) : reviewSummary(t, review)}</span>
    </div>
  );
}

/** 列表里的审核标志：只有图标，悬停看到"Autopilot · 模型审核通过"这样的说明。 */
export function ReviewMark({ review }: { review: ReviewInfo }) {
  const { t } = useTranslation();
  const { Icon, tone } = reviewIcon(review.outcome);
  const label = reviewWithMode(t, review);
  return (
    <span data-testid="review-mark" role="img" aria-label={label} title={label} className="inline-flex align-middle">
      <Icon aria-hidden className={cn("h-3.5 w-3.5", tone)} />
    </span>
  );
}

/** 每道题的评分（回答"是"的概率）和当时的阈值，审计详情里用；达到阈值的题标出来。 */
export function ReviewScores({ review }: { review: ReviewInfo }) {
  const { t } = useTranslation();
  const { scores } = review;
  if (!scores) return null;
  const failed = new Set(review.failed ?? []);
  const ids = [
    ...REVIEW_QUESTIONS.filter((q) => q in scores),
    ...Object.keys(scores).filter((q) => !REVIEW_QUESTIONS.includes(q)),
  ];
  return (
    <div data-testid="review-scores" className="text-xs">
      <div className="text-muted-foreground">
        {t("commandReview.scores")}
        {review.threshold !== undefined && ` · ${t("commandReview.scoreThreshold", { threshold: review.threshold })}`}
      </div>
      <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
        {ids.map((q) => (
          <li
            key={q}
            data-failed={failed.has(q)}
            className={cn(failed.has(q) ? "font-medium text-warning" : "text-foreground")}
          >
            {REVIEW_QUESTIONS.includes(q) ? t(`commandReview.scoreName.${q}`) : q}{" "}
            <span className="font-mono">{scores[q].toFixed(2)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
