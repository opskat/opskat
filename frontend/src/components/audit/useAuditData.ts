import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AsyncStatus } from "@/components/common/AsyncState";
import { errorMessage } from "@/lib/errorMessage";
import {
  ListAuditDeciderGroups,
  ListAuditFacets,
  ListAuditLogs,
  ListAuditSessions,
} from "../../../wailsjs/go/system/System";
import { audit_entity, audit_repo } from "../../../wailsjs/go/models";
import { buildAuditQuery, resolveTimeRange, type AuditFilters, type AuditTimeRange } from "./auditFilters";

/**
 * 按 key 拉取数据：key 变化（含首次）或调用 reload 时重新请求。只采用最后一次发起的请求的
 * 结果，较早发起、较晚返回的不会覆盖。重新请求期间保留上一次成功的数据、loading 为 true；
 * 请求失败时清掉旧数据（免得被当成这次的结果），失败后再请求则回到 loading 状态。
 */
export function useLatestFetch<T>(key: string, fetcher: () => Promise<T>, initial: T) {
  const [data, setData] = useState<T>(initial);
  const [status, setStatus] = useState<AsyncStatus>("loading");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [prevKey, setPrevKey] = useState<string | null>(null);
  // key 变化（含首次挂载）时在渲染期置 loading，effect 里只发请求
  if (prevKey !== key) {
    setPrevKey(key);
    setLoading(true);
  }
  const initialRef = useRef(initial);
  const fetcherRef = useRef(fetcher);
  useEffect(() => {
    fetcherRef.current = fetcher;
  });
  const latest = useRef(0);

  const run = useCallback(() => {
    const id = ++latest.current;
    fetcherRef.current().then(
      (value) => {
        if (id !== latest.current) return;
        setData(value);
        setError(null);
        setStatus("ready");
        setLoading(false);
      },
      (e) => {
        if (id !== latest.current) return;
        setData(initialRef.current);
        setError(errorMessage(e));
        setStatus("error");
        setLoading(false);
      }
    );
  }, []);

  useEffect(() => {
    run();
  }, [key, run]);

  const reload = useCallback(() => {
    setLoading(true);
    run();
  }, [run]);

  return { data, status: loading && status === "error" ? ("loading" as const) : status, error, loading, reload };
}

export interface AuditLogsResult {
  items: audit_entity.AuditLog[];
  total: number;
}

const EMPTY_LOGS: AuditLogsResult = { items: [], total: 0 };

/** 审计记录分页查询 */
export function useAuditLogs(filters: AuditFilters, range: AuditTimeRange, page: number, pageSize: number) {
  const key = JSON.stringify([filters, range, page, pageSize]);
  return useLatestFetch<AuditLogsResult>(
    key,
    () =>
      ListAuditLogs(buildAuditQuery(filters, range, page, pageSize)).then((r) => ({ items: r.items, total: r.total })),
    EMPTY_LOGS
  );
}

export interface AuditFacets {
  tools: string[];
  deciders: string[];
}

const EMPTY_FACETS: AuditFacets = { tools: [], deciders: [] };

/** 时间范围内出现过的工具与决策方分组（筛选项） */
export function useAuditFacets(range: AuditTimeRange) {
  const key = JSON.stringify(range);
  return useLatestFetch<AuditFacets>(
    key,
    () => {
      const { start, end } = resolveTimeRange(range);
      return ListAuditFacets(start, end).then((f) => ({ tools: f.tools, deciders: f.deciders }));
    },
    EMPTY_FACETS
  );
}

const EMPTY_SESSIONS: audit_repo.SessionInfo[] = [];

/** 时间范围内出现过的会话 */
export function useAuditSessions(range: AuditTimeRange) {
  const key = JSON.stringify(range);
  return useLatestFetch<audit_repo.SessionInfo[]>(
    key,
    () => ListAuditSessions(resolveTimeRange(range).start),
    EMPTY_SESSIONS
  );
}

/** 决策来源 → 决策方分组（分组定义在后端） */
export function useDeciderOfSource(): (source: string) => string | undefined {
  const { data } = useLatestFetch("decider-groups", () => ListAuditDeciderGroups(), []);
  const map = useMemo(() => {
    const m = new Map<string, string>();
    for (const g of data) for (const s of g.sources) m.set(s, g.id);
    return m;
  }, [data]);
  return useCallback((source: string) => map.get(source), [map]);
}
