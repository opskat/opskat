import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Calendar, Search, X } from "lucide-react";
import {
  Button,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
  cn,
} from "@opskat/ui";
import { audit_repo } from "../../../wailsjs/go/models";
import {
  TIME_PRESETS,
  activeConditionKeys,
  EMPTY_FILTERS,
  type AuditConditionKey,
  type AuditFilters,
  type AuditTimeRange,
} from "./auditFilters";
import { deciderLabel, sourceLabel } from "./auditLabels";

/** 关键字输入停止多久后才查询 */
export const KEYWORD_DEBOUNCE_MS = 300;

// 将 unix timestamp 按日期分组
function getSessionGroup(ts: number): "today" | "yesterday" | "thisWeek" | "earlier" {
  const now = new Date();
  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() / 1000;
  const yesterdayStart = todayStart - 86400;
  const weekStart = todayStart - (now.getDay() || 7) * 86400 + 86400; // 本周一
  if (ts >= todayStart) return "today";
  if (ts >= yesterdayStart) return "yesterday";
  if (ts >= weekStart) return "thisWeek";
  return "earlier";
}

const pad = (n: number) => String(n).padStart(2, "0");

function formatTimeShort(ts: number) {
  if (!ts) return "";
  const d = new Date(ts * 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatDatetimeCompact(s: string): string {
  if (!s) return "...";
  const d = new Date(s);
  return `${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * 关键字搜索框：输入停止 KEYWORD_DEBOUNCE_MS 后提交。关键字只由这里提交、由清空条件重置，
 * 清空时父组件换 key 重新挂载，连同尚未提交的输入一起丢掉
 */
function KeywordSearch({ value, onCommit }: { value: string; onCommit: (keyword: string) => void }) {
  const { t } = useTranslation();
  const [input, setInput] = useState(value);
  // 父组件每次渲染都传新的 onCommit；放进 ref，免得父组件重渲染时把计时器重置
  const onCommitRef = useRef(onCommit);
  useEffect(() => {
    onCommitRef.current = onCommit;
  });
  useEffect(() => {
    if (input === value) return;
    const timer = setTimeout(() => onCommitRef.current(input), KEYWORD_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [input, value]);

  return (
    <div className="relative flex-1 min-w-0">
      <Search className="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
      <Input
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder={t("audit.searchPlaceholder")}
        aria-label={t("audit.searchPlaceholder")}
        className="h-8 text-xs pl-8 pr-7"
      />
      {input && (
        <button
          type="button"
          aria-label={t("audit.clearSearch")}
          onClick={() => {
            setInput("");
            onCommit("");
          }}
          className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}

function SessionSelect({
  value,
  sessions,
  onChange,
}: {
  value: string;
  sessions: audit_repo.SessionInfo[];
  onChange: (session: string) => void;
}) {
  const { t } = useTranslation();
  const grouped = useMemo(() => {
    const groups: Record<string, audit_repo.SessionInfo[]> = {};
    for (const s of sessions) (groups[getSessionGroup(s.last_time)] ??= []).push(s);
    return groups;
  }, [sessions]);
  const groupLabels = {
    today: t("audit.sessionGroupToday"),
    yesterday: t("audit.sessionGroupYesterday"),
    thisWeek: t("audit.sessionGroupThisWeek"),
    earlier: t("audit.sessionGroupEarlier"),
  };
  return (
    <Select value={value || "all"} onValueChange={(v) => onChange(v === "all" ? "" : v)}>
      <SelectTrigger className="w-40 h-8 text-xs font-mono shrink-0" aria-label={t("audit.sessionID")}>
        <SelectValue placeholder={t("audit.sessionAll")} />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="all">{t("audit.sessionAll")}</SelectItem>
        {(["today", "yesterday", "thisWeek", "earlier"] as const).map((group) => {
          const items = grouped[group];
          if (!items?.length) return null;
          return (
            <SelectGroup key={group}>
              <SelectLabel>{groupLabels[group]}</SelectLabel>
              {items.map((s) => (
                <SelectItem key={s.session_id} value={s.session_id}>
                  <span className="truncate">{s.session_id.slice(0, 8)}</span>
                  <span className="text-muted-foreground ml-1">
                    {formatTimeShort(s.first_time)} ({t("audit.sessionOps", { count: s.count })})
                  </span>
                </SelectItem>
              ))}
            </SelectGroup>
          );
        })}
      </SelectContent>
    </Select>
  );
}

function TimeRangePicker({ range, onChange }: { range: AuditTimeRange; onChange: (range: AuditTimeRange) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const label =
    range.preset === "custom"
      ? `${formatDatetimeCompact(range.customStart)} - ${formatDatetimeCompact(range.customEnd)}`
      : range.preset === "0"
        ? t("audit.timeAll")
        : t(`audit.time${range.preset}`);
  const inputClass =
    "flex-1 text-xs border rounded-md px-2 py-1.5 bg-background outline-none focus-visible:ring-1 focus-visible:ring-ring/45";
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="h-8 text-xs gap-1.5 font-normal shrink-0">
          <Calendar className="h-3.5 w-3.5 text-muted-foreground" />
          {label}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-3" align="end">
        <div className="flex flex-wrap gap-1.5 mb-3">
          {[
            { value: "0", label: t("audit.timeAll") },
            ...TIME_PRESETS.map((p) => ({ value: p.value, label: t(`audit.time${p.value}`) })),
          ].map((p) => (
            <Button
              key={p.value}
              variant={range.preset === p.value ? "default" : "outline"}
              size="sm"
              className="h-7 text-xs px-2.5"
              onClick={() => {
                onChange({ ...range, preset: p.value });
                setOpen(false);
              }}
            >
              {p.label}
            </Button>
          ))}
        </div>
        <div className="border-t mb-3" />
        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <label className="text-xs text-muted-foreground shrink-0 w-10">{t("audit.timeStart")}</label>
            <input
              type="datetime-local"
              value={range.customStart}
              onChange={(e) => onChange({ ...range, preset: "custom", customStart: e.target.value })}
              className={inputClass}
            />
          </div>
          <div className="flex items-center gap-2">
            <label className="text-xs text-muted-foreground shrink-0 w-10">{t("audit.timeEnd")}</label>
            <input
              type="datetime-local"
              value={range.customEnd}
              onChange={(e) => onChange({ ...range, preset: "custom", customEnd: e.target.value })}
              className={inputClass}
            />
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function conditionLabel(key: AuditConditionKey, t: TFunction): string {
  switch (key) {
    case "session":
      return t("audit.sessionID");
    case "source":
      return t("audit.source");
    case "tool":
      return t("audit.toolName");
    case "assets":
      return t("audit.assetName");
    case "result":
      return t("audit.result");
    case "decision":
      return t("audit.decision");
    case "decider":
      return t("audit.decider");
  }
}

function conditionValue(key: AuditConditionKey, f: AuditFilters, t: TFunction): string {
  switch (key) {
    case "session":
      return f.session.slice(0, 8);
    case "source":
      return sourceLabel(f.source, t);
    case "tool":
      return f.tool;
    case "assets": {
      const names = f.assets
        .slice(0, 2)
        .map((a) => a.name)
        .join(", ");
      return f.assets.length > 2
        ? t("audit.assetsMore", { names, count: f.assets.length, more: f.assets.length - 2 })
        : names;
    }
    case "result":
      return f.result === "success" ? t("audit.success") : t("audit.failed");
    case "decision":
      return t(`audit.decisions.${f.decision}`);
    case "decider":
      return deciderLabel(f.decider, t);
  }
}

interface AuditFilterBarProps {
  /** 条件被清空的次数；变化时重置搜索框（含尚未提交的输入） */
  clearGeneration: number;
  filters: AuditFilters;
  onPatch: (patch: Partial<AuditFilters>) => void;
  onClear: () => void;
  range: AuditTimeRange;
  onRangeChange: (range: AuditTimeRange) => void;
  sessions: audit_repo.SessionInfo[];
}

/** 顶栏：关键字、会话、时间范围；下方为已生效条件标签行 */
export function AuditFilterBar({
  clearGeneration,
  filters,
  onPatch,
  onClear,
  range,
  onRangeChange,
  sessions,
}: AuditFilterBarProps) {
  const { t } = useTranslation();
  const keys = activeConditionKeys(filters);
  return (
    <>
      <div className="px-4 pb-2 flex items-center gap-2">
        <KeywordSearch key={clearGeneration} value={filters.keyword} onCommit={(keyword) => onPatch({ keyword })} />
        <SessionSelect value={filters.session} sessions={sessions} onChange={(session) => onPatch({ session })} />
        <TimeRangePicker range={range} onChange={onRangeChange} />
      </div>
      {keys.length > 0 && (
        <div className="px-4 pb-2 flex flex-wrap items-center gap-1.5" data-testid="audit-conditions">
          {keys.map((key) => (
            <span key={key} className="inline-flex items-center gap-1 h-6 pl-2 pr-1 rounded-md bg-primary/10 text-xs">
              <span className="text-muted-foreground">{conditionLabel(key, t)}:</span>
              <span className={cn("text-foreground", (key === "tool" || key === "session") && "font-mono")}>
                {conditionValue(key, filters, t)}
              </span>
              <button
                type="button"
                aria-label={t("audit.removeCondition", { name: conditionLabel(key, t) })}
                onClick={() => onPatch({ [key]: EMPTY_FILTERS[key] })}
                className="rounded p-0.5 text-muted-foreground hover:bg-primary/15 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
          <Button variant="link" size="sm" className="h-6 px-1 text-xs" onClick={onClear}>
            {t("audit.clearConditions")}
          </Button>
        </div>
      )}
    </>
  );
}
