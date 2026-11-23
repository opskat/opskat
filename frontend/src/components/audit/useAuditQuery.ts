import { useCallback, useState } from "react";
import {
  AUDIT_PAGE_SIZES,
  DEFAULT_TIME_RANGE,
  EMPTY_FILTERS,
  type AuditFilters,
  type AuditTimeRange,
} from "./auditFilters";

/**
 * 审计日志的查询条件（筛选、时间范围、分页）。由审计页持有，切到连接池页签再切回时条件不丢；
 * 关闭审计页即回到默认。任一条件变化都回到第 1 页。
 */
export function useAuditQuery() {
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS);
  const [range, setRange] = useState<AuditTimeRange>(DEFAULT_TIME_RANGE);
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState<number>(AUDIT_PAGE_SIZES[0]);
  // 每次清空加一：搜索框据此丢掉还在防抖中、尚未提交的关键字
  const [clearGeneration, setClearGeneration] = useState(0);

  const patchFilters = useCallback((patch: Partial<AuditFilters>) => {
    setFilters((prev) => ({ ...prev, ...patch }));
    setPage(0);
  }, []);
  const clearFilters = useCallback(() => {
    setFilters(EMPTY_FILTERS);
    setPage(0);
    setClearGeneration((n) => n + 1);
  }, []);
  const changeRange = useCallback((next: AuditTimeRange) => {
    setRange(next);
    setPage(0);
  }, []);
  const changePageSize = useCallback((size: number) => {
    setPageSize(size);
    setPage(0);
  }, []);

  return {
    filters,
    range,
    page,
    pageSize,
    clearGeneration,
    patchFilters,
    clearFilters,
    changeRange,
    setPage,
    changePageSize,
  };
}

export type AuditQuery = ReturnType<typeof useAuditQuery>;
