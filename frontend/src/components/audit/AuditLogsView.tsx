import { useCallback, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, ChevronRight, RefreshCw, SearchX } from "lucide-react";
import { Button } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import { AuditFilterBar } from "./AuditFilterBar";
import { AuditLogTable } from "./AuditLogTable";
import { AuditDetailDrawer } from "./AuditDetailDrawer";
import {
  DEFAULT_TIME_RANGE,
  EMPTY_FILTERS,
  hasAnyFilter,
  type AuditFilters,
  type AuditTimeRange,
} from "./auditFilters";
import { useAuditFacets, useAuditLogs, useAuditSessions, useDeciderOfSource } from "./useAuditData";

const PAGE_SIZE = 20;

/** 审计日志页签：顶栏筛选 + 表头筛选 + 列表 */
export function AuditLogsView({ tabs }: { tabs: ReactNode }) {
  const { t } = useTranslation();
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [range, setRange] = useState<AuditTimeRange>(DEFAULT_TIME_RANGE);
  const [page, setPage] = useState(0);
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const logs = useAuditLogs(filters, range, page, PAGE_SIZE);
  const facets = useAuditFacets(range);
  const sessions = useAuditSessions(range);
  const deciderOf = useDeciderOfSource();

  // 任一条件变化都回到第 1 页
  const patchFilters = useCallback((patch: Partial<AuditFilters>) => {
    setFilters((prev) => ({ ...prev, ...patch }));
    setPage(0);
  }, []);
  const clearFilters = useCallback(() => {
    setFilters(EMPTY_FILTERS);
    setPage(0);
  }, []);
  const changeRange = useCallback((next: AuditTimeRange) => {
    setRange(next);
    setPage(0);
  }, []);

  const refresh = () => {
    logs.reload();
    facets.reload();
    sessions.reload();
  };

  const items = logs.data.items;
  // 换页 / 换筛选后所选记录不在新结果里，就关掉详情
  const selectedIndex = selectedId === null ? -1 : items.findIndex((l) => l.ID === selectedId);
  if (selectedId !== null && selectedIndex < 0 && !logs.loading) setSelectedId(null);
  const selected = selectedIndex >= 0 ? items[selectedIndex] : null;
  const total = logs.data.total;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const filtered = hasAnyFilter(filters);

  // 会话已允许的模式（grant_submit 记录）
  const sessionApprovedPatterns = useMemo(() => {
    if (!filters.session) return [];
    return items
      .filter((l) => l.ToolName === "grant_submit" && l.Command)
      .map((l) => ({ asset: l.AssetName || "-", patterns: l.Command }));
  }, [items, filters.session]);

  const placeholder =
    logs.status === "error" || items.length === 0 ? (
      <AsyncState
        status={logs.loading ? "loading" : logs.status}
        error={logs.error}
        onRetry={logs.reload}
        className="py-12"
        empty={
          filtered ? (
            <div className="flex flex-col items-center gap-2 text-muted-foreground">
              <SearchX className="h-6 w-6" />
              <div className="text-sm">{t("audit.emptyFiltered")}</div>
              <Button variant="outline" size="sm" className="h-7 text-xs mt-1" onClick={clearFilters}>
                {t("audit.clearAllFilters")}
              </Button>
            </div>
          ) : (
            t("audit.empty")
          )
        }
      />
    ) : undefined;

  return (
    <>
      <div className="border-b">
        <div className="px-4 pt-3 pb-2 flex items-center justify-between gap-4">
          {tabs}
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">{t("audit.total", { total })}</span>
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              onClick={refresh}
              disabled={logs.loading}
              aria-label={t("audit.refresh")}
            >
              <RefreshCw className={`h-3.5 w-3.5 ${logs.loading ? "animate-spin" : ""}`} />
            </Button>
          </div>
        </div>
        <AuditFilterBar
          filters={filters}
          onPatch={patchFilters}
          onClear={clearFilters}
          range={range}
          onRangeChange={changeRange}
          sessions={sessions.data}
        />
      </div>

      {sessionApprovedPatterns.length > 0 && (
        <div className="px-4 py-2 border-b bg-info/10 text-xs">
          <span className="font-medium text-info">{t("audit.sessionPatterns")}:</span>
          <div className="flex flex-wrap gap-1.5 mt-1">
            {sessionApprovedPatterns.map((p, i) => (
              <span
                key={i}
                className="inline-flex items-center gap-1 bg-info/15 text-info px-2 py-0.5 rounded font-mono"
              >
                {p.asset !== "-" && <span className="text-info">{p.asset}:</span>}
                {p.patterns}
              </span>
            ))}
          </div>
        </div>
      )}

      <div className="relative flex-1 min-h-0 flex flex-col">
        <div className="flex-1 overflow-auto">
          <AuditLogTable
            logs={items}
            filters={filters}
            onPatch={patchFilters}
            facets={facets}
            deciderOf={deciderOf}
            selectedId={selected?.ID ?? null}
            onSelect={(log) => setSelectedId(log.ID)}
            placeholder={placeholder}
          />
        </div>
        {selected && (
          <AuditDetailDrawer
            log={selected}
            filters={filters}
            onPatch={patchFilters}
            onClose={() => setSelectedId(null)}
            onPrev={selectedIndex > 0 ? () => setSelectedId(items[selectedIndex - 1].ID) : undefined}
            onNext={selectedIndex < items.length - 1 ? () => setSelectedId(items[selectedIndex + 1].ID) : undefined}
          />
        )}
      </div>

      {totalPages > 1 && (
        <div className="px-4 py-2 border-t flex items-center justify-center gap-2">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            disabled={page === 0}
            onClick={() => setPage(page - 1)}
          >
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <span className="text-sm text-muted-foreground">
            {page + 1} / {totalPages}
          </span>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            disabled={page >= totalPages - 1}
            onClick={() => setPage(page + 1)}
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      )}
    </>
  );
}
