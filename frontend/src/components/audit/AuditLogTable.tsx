import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, XCircle } from "lucide-react";
import { cn } from "@opskat/ui";
import { AsyncState, type AsyncStatus } from "@/components/common/AsyncState";
import { AssetMultiSelect } from "@/components/asset/AssetMultiSelect";
import { ReviewMark } from "@/components/approval/ReviewNotice";
import { parseReviewInfo } from "@/lib/commandReview";
import { useAssetStore } from "@/stores/assetStore";
import { audit_entity } from "../../../wailsjs/go/models";
import { AuditHeaderFilter, AuditOptionList, AuditSegmented } from "./AuditHeaderFilter";
import { AUDIT_SOURCES, decisionSourceBadge, deciderLabel, sourceLabel } from "./auditLabels";
import { formatAuditTime, formatAuditTimeFull, type AuditFilters } from "./auditFilters";
import type { AuditFacets } from "./useAuditData";

export interface AuditFacetsLoad {
  data: AuditFacets;
  status: AsyncStatus;
  error: string | null;
  reload: () => void;
}

interface AuditLogTableProps {
  logs: audit_entity.AuditLog[];
  filters: AuditFilters;
  onPatch: (patch: Partial<AuditFilters>) => void;
  facets: AuditFacetsLoad;
  deciderOf: (source: string) => string | undefined;
  /** 有旧行时重新查询中：旧行半透明、表头下出现进度条 */
  loading: boolean;
  /** 详情抽屉里正在看的记录 */
  selectedId: number | null;
  onSelect: (log: audit_entity.AuditLog) => void;
  /** 无行可显示时占满表格宽度的一行（加载 / 错误 / 空态） */
  placeholder?: ReactNode;
}

const COLUMN_COUNT = 7;

/** 悬停用的错误摘要：首行，过长截断 */
const ERROR_SUMMARY_MAX = 200;
function errorSummary(error: string): string {
  const line = error.trim().split("\n")[0];
  return line.length > ERROR_SUMMARY_MAX ? `${line.slice(0, ERROR_SUMMARY_MAX)}…` : line;
}

/** 可点的单元格内容：点了即按该值筛选 */
function FilterCell({
  children,
  onClick,
  mono,
  title,
}: {
  children: ReactNode;
  onClick: () => void;
  mono?: boolean;
  title: string;
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={cn(
        "text-left max-w-full truncate cursor-pointer hover:underline underline-offset-2 decoration-dotted hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring rounded-sm",
        mono && "font-mono"
      )}
    >
      {children}
    </button>
  );
}

/** 筛选项还没拿到 / 拿失败时，弹层里显示加载态或错误与重试 */
function FacetGate({ facets, children }: { facets: AuditFacetsLoad; children: ReactNode }) {
  if (facets.status === "ready") return <>{children}</>;
  return <AsyncState status={facets.status} error={facets.error} onRetry={facets.reload} className="py-4" />;
}

export function AuditLogTable({
  logs,
  filters,
  onPatch,
  facets,
  deciderOf,
  loading,
  selectedId,
  onSelect,
  placeholder,
}: AuditLogTableProps) {
  const { t } = useTranslation();
  const assets = useAssetStore((s) => s.assets);
  const filterThis = t("audit.filterThis");

  const pickAssets = (ids: number[]) => {
    onPatch({
      assets: ids.map((id) => {
        const known = filters.assets.find((a) => a.id === id);
        return known ?? { id, name: assets.find((a) => a.ID === id)?.Name ?? `#${id}` };
      }),
    });
  };

  const addAsset = (log: audit_entity.AuditLog) => {
    if (filters.assets.some((a) => a.id === log.AssetID)) return;
    onPatch({ assets: [...filters.assets, { id: log.AssetID, name: log.AssetName || `#${log.AssetID}` }] });
  };

  return (
    <table className="w-full text-sm">
      <thead className="sticky top-0 z-10 bg-background border-b">
        <tr className="text-left text-muted-foreground whitespace-nowrap">
          <th className="px-4 py-2 font-medium">{t("audit.time")}</th>
          <AuditHeaderFilter label={t("audit.source")} testId="audit-filter-source" active={!!filters.source}>
            {(close) => (
              <AuditOptionList
                value={filters.source}
                options={AUDIT_SOURCES.map((s) => ({ value: s, label: sourceLabel(s, t) }))}
                onPick={(source) => {
                  onPatch({ source });
                  close();
                }}
              />
            )}
          </AuditHeaderFilter>
          <AuditHeaderFilter label={t("audit.toolName")} testId="audit-filter-tool" active={!!filters.tool}>
            {(close) => (
              <FacetGate facets={facets}>
                <AuditOptionList
                  mono
                  value={filters.tool}
                  options={facets.data.tools.map((tool) => ({ value: tool, label: tool }))}
                  onPick={(tool) => {
                    onPatch({ tool });
                    close();
                  }}
                />
              </FacetGate>
            )}
          </AuditHeaderFilter>
          <AuditHeaderFilter
            label={t("audit.assetName")}
            testId="audit-filter-asset"
            active={filters.assets.length > 0}
            contentClassName="w-64"
          >
            {() => (
              <AssetMultiSelect
                values={filters.assets.map((a) => a.id)}
                onValuesChange={pickAssets}
                activeOnly={false}
                searchPlaceholder={t("audit.searchAssets")}
                className="max-h-72"
              />
            )}
          </AuditHeaderFilter>
          <th className="px-4 py-2 font-medium">{t("audit.command")}</th>
          <AuditHeaderFilter
            label={t("audit.decision")}
            testId="audit-filter-decision"
            active={!!filters.decision || !!filters.decider}
            align="end"
            contentClassName="w-52"
          >
            {() => (
              <div className="space-y-2 p-0.5">
                <AuditSegmented
                  value={filters.decision}
                  options={[
                    { value: "allow", label: t("audit.decisions.allow") },
                    { value: "deny", label: t("audit.decisions.deny") },
                  ]}
                  onChange={(v) => onPatch({ decision: v as AuditFilters["decision"] })}
                />
                <div className="border-t pt-1.5">
                  <div className="px-1 pb-0.5 text-[11px] text-muted-foreground">{t("audit.decider")}</div>
                  <FacetGate facets={facets}>
                    <AuditOptionList
                      value={filters.decider}
                      options={facets.data.deciders.map((g) => ({ value: g, label: deciderLabel(g, t) }))}
                      onPick={(decider) => onPatch({ decider })}
                    />
                  </FacetGate>
                </div>
              </div>
            )}
          </AuditHeaderFilter>
          <AuditHeaderFilter
            label={t("audit.result")}
            testId="audit-filter-result"
            active={!!filters.result}
            hideLabel
            align="end"
            className="w-10 px-2 text-center"
            contentClassName="w-32"
          >
            {(close) => (
              <AuditOptionList
                value={filters.result}
                options={[
                  { value: "success", label: t("audit.success") },
                  { value: "failed", label: t("audit.failed") },
                ]}
                onPick={(v) => {
                  onPatch({ result: v as AuditFilters["result"] });
                  close();
                }}
              />
            )}
          </AuditHeaderFilter>
        </tr>
        {loading && (
          <tr>
            <td colSpan={COLUMN_COUNT} className="p-0">
              <div role="progressbar" aria-label={t("audit.reloading")} className="h-0.5 overflow-hidden bg-primary/15">
                <div className="h-full w-1/3 bg-primary animate-indeterminate-progress motion-reduce:w-full motion-reduce:animate-none" />
              </div>
            </td>
          </tr>
        )}
      </thead>
      <tbody
        aria-busy={loading}
        className={cn("select-text transition-opacity", loading && "opacity-50 pointer-events-none")}
      >
        {placeholder && (
          <tr>
            <td colSpan={COLUMN_COUNT}>{placeholder}</td>
          </tr>
        )}
        {logs.map((log) => {
          const badge = decisionSourceBadge(log.DecisionSource, t);
          const review = log.Review ? parseReviewInfo(log.Review) : null;
          const decider = deciderOf(log.DecisionSource);
          return (
            <tr
              key={log.ID}
              aria-selected={selectedId === log.ID}
              onClick={() => {
                // 拖选文字复制时不打开详情
                if (window.getSelection()?.toString()) return;
                onSelect(log);
              }}
              className={cn(
                "border-b cursor-pointer transition-colors",
                selectedId === log.ID ? "bg-primary/10" : "hover:bg-muted/50"
              )}
            >
              <td
                className="px-4 py-2 text-xs text-muted-foreground whitespace-nowrap tabular-nums"
                title={formatAuditTimeFull(log.Createtime)}
              >
                {formatAuditTime(log.Createtime)}
              </td>
              <td className="px-4 py-2 text-xs whitespace-nowrap">
                <FilterCell title={filterThis} onClick={() => onPatch({ source: log.Source })}>
                  {sourceLabel(log.Source, t)}
                </FilterCell>
              </td>
              <td className="px-4 py-2 text-xs">
                <FilterCell mono title={filterThis} onClick={() => onPatch({ tool: log.ToolName })}>
                  {log.ToolName}
                </FilterCell>
              </td>
              <td className="px-4 py-2 whitespace-nowrap">
                {/* 限宽：资产名再长也不把表格撑出横向滚动，全名在详情里 */}
                <div className="max-w-40 truncate">
                  {log.AssetID > 0 ? (
                    <FilterCell title={filterThis} onClick={() => addAsset(log)}>
                      {log.AssetName || `#${log.AssetID}`}
                    </FilterCell>
                  ) : (
                    log.AssetName || <span className="text-muted-foreground">-</span>
                  )}
                </div>
              </td>
              <td className="px-4 py-2 font-mono text-xs truncate w-full max-w-0" title={log.Command}>
                {log.Command || "-"}
              </td>
              <td className="px-4 py-2 whitespace-nowrap">
                {log.DecisionSource ? (
                  <button
                    type="button"
                    title={
                      log.MatchedPattern
                        ? `${t("audit.matchedPattern")}: ${log.MatchedPattern}\n${filterThis}`
                        : filterThis
                    }
                    onClick={(e) => {
                      e.stopPropagation();
                      onPatch({ decision: log.Decision as AuditFilters["decision"], decider: decider ?? "" });
                    }}
                    className={`inline-block px-1.5 py-0.5 text-xs rounded font-mono cursor-pointer hover:underline underline-offset-2 decoration-dotted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${badge.className}`}
                  >
                    {log.Decision === "allow" ? "✓" : "✗"} {badge.label}
                  </button>
                ) : (
                  <span className="text-muted-foreground">-</span>
                )}
                {review && (
                  <span className="ml-1">
                    <ReviewMark review={review} />
                  </span>
                )}
              </td>
              <td className="px-2 py-2 text-center">
                <button
                  type="button"
                  title={log.Success !== 1 && log.Error ? `${errorSummary(log.Error)}\n${filterThis}` : filterThis}
                  onClick={(e) => {
                    e.stopPropagation();
                    onPatch({ result: log.Success === 1 ? "success" : "failed" });
                  }}
                  className="cursor-pointer rounded-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                >
                  {log.Success === 1 ? (
                    <CheckCircle2 aria-label={t("audit.success")} className="h-4 w-4 text-success" />
                  ) : (
                    <XCircle aria-label={t("audit.failed")} className="h-4 w-4 text-destructive" />
                  )}
                </button>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
