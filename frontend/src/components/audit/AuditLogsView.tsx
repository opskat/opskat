import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw, SearchX } from "lucide-react";
import { Button } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import { AuditFilterBar } from "./AuditFilterBar";
import { AuditLogTable } from "./AuditLogTable";
import { AuditDetailDrawer } from "./AuditDetailDrawer";
import { AuditPager } from "./AuditPager";
import { AuditSessionGrants } from "./AuditSessionGrants";
import { hasAnyFilter } from "./auditFilters";
import { useAuditFacets, useAuditLogs, useAuditSessions, useDeciderOfSource } from "./useAuditData";
import type { AuditQuery } from "./useAuditQuery";

/** 审计日志页签：顶栏筛选 + 表头筛选 + 列表 */
export function AuditLogsView({ tabs, query }: { tabs: ReactNode; query: AuditQuery }) {
  const { t } = useTranslation();
  const { filters, range, page, pageSize, patchFilters, clearFilters, changeRange, setPage, changePageSize } = query;
  const [selectedId, setSelectedId] = useState<number | null>(null);
  // 刷新按钮递增，会话授权汇总据此重新拉取
  const [refreshKey, setRefreshKey] = useState(0);

  const logs = useAuditLogs(filters, range, page, pageSize);
  const facets = useAuditFacets(range);
  const sessions = useAuditSessions(range);
  const deciderOf = useDeciderOfSource();

  const refresh = () => {
    logs.reload();
    facets.reload();
    sessions.reload();
    setRefreshKey((k) => k + 1);
  };

  const items = logs.data.items;
  // 换页 / 换筛选后所选记录不在新结果里，就关掉详情
  const selectedIndex = selectedId === null ? -1 : items.findIndex((l) => l.ID === selectedId);
  if (selectedId !== null && selectedIndex < 0 && !logs.loading) setSelectedId(null);
  const selected = selectedIndex >= 0 ? items[selectedIndex] : null;
  const total = logs.data.total;
  const filtered = hasAnyFilter(filters);

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

      {filters.session && <AuditSessionGrants sessionId={filters.session} refreshKey={refreshKey} />}

      <div className="relative flex-1 min-h-0 flex flex-col">
        <div className="flex-1 overflow-auto">
          <AuditLogTable
            logs={items}
            filters={filters}
            onPatch={patchFilters}
            facets={facets}
            deciderOf={deciderOf}
            loading={logs.loading && items.length > 0}
            selectedId={selected?.ID ?? null}
            onSelect={(log) => setSelectedId(log.ID)}
            placeholder={placeholder}
          />
        </div>
        {selected && (
          <AuditDetailDrawer
            log={selected}
            filters={filters}
            deciderOf={deciderOf}
            onPatch={patchFilters}
            onClose={() => setSelectedId(null)}
            onPrev={selectedIndex > 0 ? () => setSelectedId(items[selectedIndex - 1].ID) : undefined}
            onNext={selectedIndex < items.length - 1 ? () => setSelectedId(items[selectedIndex + 1].ID) : undefined}
          />
        )}
      </div>

      <AuditPager
        page={page}
        pageSize={pageSize}
        total={total}
        loading={logs.loading}
        onPage={setPage}
        onPageSize={changePageSize}
      />
    </>
  );
}
