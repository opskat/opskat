import { useTranslation } from "react-i18next";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@opskat/ui";
import { ReviewNotice, ReviewScores } from "@/components/approval/ReviewNotice";
import { parseReviewInfo } from "@/lib/commandReview";
import { audit_entity } from "../../../wailsjs/go/models";
import { decisionLabel, decisionSourceBadge } from "./auditLabels";
import { formatAuditTimeFull } from "./auditFilters";

// 审计记录里的模型审核结果（后端存的 JSON）；解析不了就原样显示，不隐藏。
export function AuditReview({ raw }: { raw: string }) {
  const { t } = useTranslation();
  const review = parseReviewInfo(raw);
  if (!review) return <code className="font-mono text-xs break-all">{raw}</code>;
  return (
    <div className="space-y-1.5">
      <ReviewNotice review={review} withMode className="text-sm" />
      <ReviewScores review={review} />
      {review.extension && (
        <p data-testid="review-reviewer" className="text-xs text-muted-foreground">
          {t("commandReview.reviewedBy", { name: review.extension, version: review.version })}
        </p>
      )}
      {review.attempts !== undefined && review.attempts > 1 && (
        <p data-testid="review-attempts" className="text-xs text-muted-foreground">
          {t("commandReview.retried", { attempts: review.attempts })}
        </p>
      )}
    </div>
  );
}

export function AuditDetailDialog({ log, onClose }: { log: audit_entity.AuditLog | null; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={!!log} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("audit.detail")}</DialogTitle>
        </DialogHeader>
        {log && (
          <div className="space-y-3 text-sm select-text">
            <div className="grid grid-cols-2 gap-2">
              <div>
                <span className="text-muted-foreground">{t("audit.toolName")}:</span>{" "}
                <span className="font-mono">{log.ToolName}</span>
              </div>
              <div>
                <span className="text-muted-foreground">{t("audit.assetName")}:</span> {log.AssetName || "-"}
              </div>
              <div>
                <span className="text-muted-foreground">{t("audit.result")}:</span>{" "}
                {log.Success === 1 ? (
                  <span className="text-success">{t("audit.success")}</span>
                ) : (
                  <span className="text-destructive">{t("audit.failed")}</span>
                )}
              </div>
              <div>
                <span className="text-muted-foreground">{t("audit.decision")}:</span>{" "}
                <span className="font-mono">{decisionLabel(log.Decision, t)}</span>
              </div>
              <div>
                <span className="text-muted-foreground">{t("audit.decisionSource")}:</span>{" "}
                <span className="font-mono">{decisionSourceBadge(log.DecisionSource, t).label}</span>
              </div>
              {log.MatchedPattern && (
                <div className="col-span-2">
                  <span className="text-muted-foreground">{t("audit.matchedPattern")}:</span>{" "}
                  <code className="font-mono bg-muted px-1 rounded">{log.MatchedPattern}</code>
                </div>
              )}
              {log.Review && (
                <div className="col-span-2 flex items-start gap-1">
                  <span className="shrink-0 text-muted-foreground">{t("commandReview.auditLabel")}:</span>
                  <AuditReview raw={log.Review} />
                </div>
              )}
              {log.SessionID && (
                <div className="col-span-2">
                  <span className="text-muted-foreground">{t("audit.sessionID")}:</span>{" "}
                  <code className="font-mono text-xs bg-muted px-1 rounded">{log.SessionID}</code>
                </div>
              )}
              <div className="col-span-2">
                <span className="text-muted-foreground">{t("audit.time")}:</span> {formatAuditTimeFull(log.Createtime)}
              </div>
            </div>
            {log.Command && (
              <div>
                <div className="text-muted-foreground mb-1">{t("audit.command")}</div>
                <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all select-text">
                  {log.Command}
                </pre>
              </div>
            )}
            {log.Request && (
              <div>
                <div className="text-muted-foreground mb-1">{t("audit.request")}</div>
                <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all max-h-40 overflow-y-auto select-text">
                  {log.Request}
                </pre>
              </div>
            )}
            {log.Result && (
              <div>
                <div className="text-muted-foreground mb-1">{t("audit.response")}</div>
                <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all max-h-40 overflow-y-auto select-text">
                  {log.Result}
                </pre>
              </div>
            )}
            {log.Error && (
              <div>
                <div className="text-muted-foreground mb-1">{t("audit.error")}</div>
                <pre className="bg-muted p-2 rounded text-xs font-mono whitespace-pre-wrap break-all text-destructive select-text">
                  {log.Error}
                </pre>
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
