import { audit_svc } from "../../../wailsjs/go/models";

/** 资产筛选项：名称取自资产树或审计行，已删除的资产也能显示 */
export interface AuditAssetRef {
  id: number;
  name: string;
}

/** 审计页的筛选条件（时间范围、分页单独管理） */
export interface AuditFilters {
  keyword: string;
  source: string;
  tool: string;
  assets: AuditAssetRef[];
  result: "" | "success" | "failed";
  decision: "" | "allow" | "deny";
  decider: string;
  session: string;
}

export const EMPTY_FILTERS: AuditFilters = {
  keyword: "",
  source: "",
  tool: "",
  assets: [],
  result: "",
  decision: "",
  decider: "",
  session: "",
};

/** 条件标签行里的一项：key 对应 AuditFilters 的字段，移除时恢复该字段的空值 */
export type AuditConditionKey = Exclude<keyof AuditFilters, "keyword">;

/** 有值的条件字段，按标签行顺序 */
export function activeConditionKeys(f: AuditFilters): AuditConditionKey[] {
  const keys: AuditConditionKey[] = [];
  if (f.session) keys.push("session");
  if (f.source) keys.push("source");
  if (f.tool) keys.push("tool");
  if (f.assets.length > 0) keys.push("assets");
  if (f.result) keys.push("result");
  if (f.decision) keys.push("decision");
  if (f.decider) keys.push("decider");
  return keys;
}

export function hasAnyFilter(f: AuditFilters): boolean {
  return f.keyword !== "" || activeConditionKeys(f).length > 0;
}

// 时间范围预设（秒）
export const TIME_PRESETS = [
  { value: "1h", seconds: 3600 },
  { value: "3h", seconds: 10800 },
  { value: "6h", seconds: 21600 },
  { value: "1d", seconds: 86400 },
  { value: "7d", seconds: 604800 },
] as const;

/** 时间范围：预设值、"0"（全部）或 "custom"（自定义起止，datetime-local 字符串） */
export interface AuditTimeRange {
  preset: string;
  customStart: string;
  customEnd: string;
}

export const DEFAULT_TIME_RANGE: AuditTimeRange = { preset: "1d", customStart: "", customEnd: "" };

function fromDatetimeLocal(s: string): number {
  if (!s) return 0;
  return Math.floor(new Date(s).getTime() / 1000);
}

/** 按当前时刻把时间范围换算成 unix 秒；0 表示不限制 */
export function resolveTimeRange(range: AuditTimeRange, nowMs = Date.now()): { start: number; end: number } {
  if (range.preset === "custom") {
    return { start: fromDatetimeLocal(range.customStart), end: fromDatetimeLocal(range.customEnd) };
  }
  const preset = TIME_PRESETS.find((p) => p.value === range.preset);
  return { start: preset ? Math.floor(nowMs / 1000) - preset.seconds : 0, end: 0 };
}

/** 组装后端查询 */
export function buildAuditQuery(
  f: AuditFilters,
  range: AuditTimeRange,
  page: number,
  pageSize: number,
  nowMs = Date.now()
): audit_svc.Query {
  const { start, end } = resolveTimeRange(range, nowMs);
  return audit_svc.Query.createFrom({
    keyword: f.keyword,
    source: f.source,
    tool_name: f.tool,
    asset_ids: f.assets.map((a) => a.id),
    result: f.result,
    decision: f.decision,
    decider: f.decider,
    session_id: f.session,
    start_time: start,
    end_time: end,
    offset: page * pageSize,
    limit: pageSize,
  });
}

const pad = (n: number) => String(n).padStart(2, "0");

/** 表格时间：与当前同年省略年份 */
export function formatAuditTime(ts: number, now = new Date()): string {
  if (!ts) return "-";
  const d = new Date(ts * 1000);
  const time = `${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  return d.getFullYear() === now.getFullYear() ? time : `${d.getFullYear()}/${time}`;
}

/** 完整时间（悬停与详情） */
export function formatAuditTimeFull(ts: number): string {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}
