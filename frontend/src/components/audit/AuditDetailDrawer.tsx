import { useEffect, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { CheckCircle2, ChevronDown, ChevronUp, Copy, X, XCircle } from "lucide-react";
import { Button, cn } from "@opskat/ui";
import { ReviewNotice, ReviewScores } from "@/components/approval/ReviewNotice";
import { parseReviewInfo } from "@/lib/commandReview";
import { errorMessage } from "@/lib/errorMessage";
import { notifyCopied } from "@/lib/notify";
import { audit_entity } from "../../../wailsjs/go/models";
import { decisionLabel, decisionSourceBadge, deciderLabel, sourceLabel } from "./auditLabels";
import { formatAuditTimeFull, type AuditFilters } from "./auditFilters";

// 审计记录里的模型审核结果（后端存的 JSON）；解析不了就原样显示，不隐藏。
function AuditReview({ raw }: { raw: string }) {
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

/** 请求是 JSON 时缩进显示，否则原样 */
function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function useCopy() {
  const { t } = useTranslation();
  return async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      notifyCopied(t("action.copied"));
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };
}

function CopyBlock({ title, body, tone }: { title: string; body: string; tone?: "error" }) {
  const { t } = useTranslation();
  const copy = useCopy();
  return (
    <section>
      <div className="flex items-center justify-between mb-1">
        <span className="text-xs text-muted-foreground">{title}</span>
        <Button
          variant="ghost"
          size="icon"
          className="h-6 w-6"
          aria-label={t("audit.copyField", { name: title })}
          onClick={() => void copy(body)}
        >
          <Copy className="h-3.5 w-3.5" />
        </Button>
      </div>
      <pre
        className={cn(
          "rounded-md p-2 text-xs font-mono whitespace-pre-wrap break-all max-h-48 overflow-auto select-text",
          tone === "error" ? "bg-destructive/10 text-destructive" : "bg-muted"
        )}
      >
        {body}
      </pre>
    </section>
  );
}

function Meta({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

/** 详情里可点的值：点了即加为筛选条件 */
function FilterValue({ children, onClick, mono }: { children: ReactNode; onClick: () => void; mono?: boolean }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      title={t("audit.filterThis")}
      onClick={onClick}
      className={cn(
        "text-left hover:underline underline-offset-2 decoration-dotted hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring rounded-sm",
        mono && "font-mono"
      )}
    >
      {children}
    </button>
  );
}

interface AuditDetailDrawerProps {
  log: audit_entity.AuditLog;
  filters: AuditFilters;
  onPatch: (patch: Partial<AuditFilters>) => void;
  onClose: () => void;
  onPrev?: () => void;
  onNext?: () => void;
}

/** 键盘事件来自可编辑元素时不当作导航键 */
function fromEditable(e: KeyboardEvent): boolean {
  const el = e.target as HTMLElement | null;
  return !!el && (el.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(el.tagName));
}

/** 表格右侧的非模态详情面板；↑ / ↓ 切换当前页内的记录，Esc 关闭 */
export function AuditDetailDrawer({ log, filters, onPatch, onClose, onPrev, onNext }: AuditDetailDrawerProps) {
  const { t } = useTranslation();
  const copy = useCopy();
  const handlers = useRef({ onClose, onPrev, onNext });
  useEffect(() => {
    handlers.current = { onClose, onPrev, onNext };
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || fromEditable(e)) return;
      if (e.key === "Escape") handlers.current.onClose();
      else if (e.key === "ArrowDown" && handlers.current.onNext) {
        e.preventDefault();
        handlers.current.onNext();
      } else if (e.key === "ArrowUp" && handlers.current.onPrev) {
        e.preventDefault();
        handlers.current.onPrev();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const badge = decisionSourceBadge(log.DecisionSource, t);
  const addAsset = () => {
    if (filters.assets.some((a) => a.id === log.AssetID)) return;
    onPatch({ assets: [...filters.assets, { id: log.AssetID, name: log.AssetName || `#${log.AssetID}` }] });
  };

  return (
    <aside
      aria-label={t("audit.detail")}
      data-testid="audit-detail"
      className="absolute inset-y-0 right-0 z-20 w-[440px] max-w-[85%] bg-background border-l shadow-lg flex flex-col"
    >
      <header className="h-11 px-3 border-b flex items-center gap-2 shrink-0">
        {log.Success === 1 ? (
          <CheckCircle2 aria-label={t("audit.success")} className="h-4 w-4 shrink-0 text-success" />
        ) : (
          <XCircle aria-label={t("audit.failed")} className="h-4 w-4 shrink-0 text-destructive" />
        )}
        <span className="font-mono text-sm truncate">{log.ToolName}</span>
        {log.AssetName && <span className="text-xs text-muted-foreground truncate">· {log.AssetName}</span>}
        <div className="ml-auto flex items-center shrink-0">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            disabled={!onPrev}
            onClick={onPrev}
            aria-label={t("audit.prevRecord")}
            title={`${t("audit.prevRecord")} (↑)`}
          >
            <ChevronUp className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            disabled={!onNext}
            onClick={onNext}
            aria-label={t("audit.nextRecord")}
            title={`${t("audit.nextRecord")} (↓)`}
          >
            <ChevronDown className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            onClick={onClose}
            aria-label={t("audit.closeDetail")}
            title={`${t("audit.closeDetail")} (Esc)`}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      </header>
      <div className="flex-1 overflow-auto p-4 space-y-4 text-sm select-text">
        <dl className="grid grid-cols-[5rem_1fr] gap-x-3 gap-y-2 text-xs">
          <Meta label={t("audit.time")}>{formatAuditTimeFull(log.Createtime)}</Meta>
          <Meta label={t("audit.source")}>
            <FilterValue onClick={() => onPatch({ source: log.Source })}>{sourceLabel(log.Source, t)}</FilterValue>
          </Meta>
          <Meta label={t("audit.assetName")}>
            {log.AssetID > 0 ? (
              <FilterValue onClick={addAsset}>{log.AssetName || `#${log.AssetID}`}</FilterValue>
            ) : (
              log.AssetName || "-"
            )}
          </Meta>
          <Meta label={t("audit.decision")}>
            {log.DecisionSource ? (
              <span className={cn("inline-block px-1.5 py-0.5 rounded font-mono", badge.className)}>
                {log.Decision === "allow" ? "✓" : "✗"} {decisionLabel(log.Decision, t)} · {badge.label}
              </span>
            ) : (
              <span className="text-muted-foreground">{deciderLabel("none", t)}</span>
            )}
          </Meta>
          {log.MatchedPattern && (
            <Meta label={t("audit.matchedPattern")}>
              <code className="font-mono bg-muted px-1 rounded break-all">{log.MatchedPattern}</code>
            </Meta>
          )}
          {log.Review && (
            <Meta label={t("commandReview.auditLabel")}>
              <AuditReview raw={log.Review} />
            </Meta>
          )}
          {log.SessionID && (
            <Meta label={t("audit.sessionID")}>
              <span className="inline-flex items-center gap-1 min-w-0 max-w-full">
                <FilterValue mono onClick={() => onPatch({ session: log.SessionID })}>
                  <span className="break-all">{log.SessionID}</span>
                </FilterValue>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-5 w-5 shrink-0"
                  aria-label={t("audit.copyField", { name: t("audit.sessionID") })}
                  onClick={() => void copy(log.SessionID)}
                >
                  <Copy className="h-3 w-3" />
                </Button>
              </span>
            </Meta>
          )}
        </dl>
        {log.Command && <CopyBlock title={t("audit.command")} body={log.Command} />}
        {log.Error && <CopyBlock title={t("audit.error")} body={log.Error} tone="error" />}
        {log.Request && <CopyBlock title={t("audit.request")} body={prettyJSON(log.Request)} />}
        {log.Result && <CopyBlock title={t("audit.response")} body={log.Result} />}
      </div>
    </aside>
  );
}
