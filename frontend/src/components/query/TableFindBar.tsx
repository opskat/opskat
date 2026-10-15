import { useEffect, type ReactNode, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { X, ChevronUp, ChevronDown, CaseSensitive, WholeWord, Regex, Search } from "lucide-react";
import { cn, Input, Button } from "@opskat/ui";
import type { TableFindOptions, TableFindRange } from "@/lib/tableFind";

/** 计数区的状态：未输入 / 正则无效 / 无结果 / 当前第几个（null = 选中格不是命中）。 */
export type TableFindStatus =
  | { kind: "idle" }
  | { kind: "invalidRegex" }
  | { kind: "none" }
  | { kind: "count"; current: number | null; total: number };

interface TableFindBarProps {
  inputRef: RefObject<HTMLInputElement | null>;
  /** 每次递增都把焦点放回输入框并全选关键词（⌘F 再次按下时保留关键词）。 */
  focusRequest: number;
  query: string;
  onQueryChange: (query: string) => void;
  options: TableFindOptions;
  onOptionsChange: (options: TableFindOptions) => void;
  status: TableFindStatus;
  scope?: { rowCount: number; page: number };
  onNext: () => void;
  onPrevious: () => void;
  onClose: () => void;
}

/** 表格内查找条：结构与 TerminalSearchBar 一致（输入框 + 三个开关 + 上下一个 + 关闭），另加命中计数与范围说明。 */
export function TableFindBar({
  inputRef,
  focusRequest,
  query,
  onQueryChange,
  options,
  onOptionsChange,
  status,
  scope,
  onNext,
  onPrevious,
  onClose,
}: TableFindBarProps) {
  const { t } = useTranslation();

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [focusRequest, inputRef]);

  const invalid = status.kind === "invalidRegex" || status.kind === "none";
  const statusText =
    status.kind === "invalidRegex"
      ? t("query.find.invalidRegex")
      : status.kind === "none"
        ? t("query.find.noResults")
        : status.kind === "count"
          ? `${status.current ?? "?"} / ${status.total}`
          : null;

  const toggles: { key: keyof TableFindOptions; label: string; icon: ReactNode }[] = [
    { key: "caseSensitive", label: t("query.find.caseSensitive"), icon: <CaseSensitive className="h-3.5 w-3.5" /> },
    { key: "wholeCell", label: t("query.find.wholeCell"), icon: <WholeWord className="h-3.5 w-3.5" /> },
    { key: "regex", label: t("query.find.regex"), icon: <Regex className="h-3.5 w-3.5" /> },
  ];

  return (
    <div
      className="flex items-center gap-1 px-2 py-1.5 border-b bg-background/95 backdrop-blur-sm shrink-0"
      onKeyDown={(e) => {
        // 焦点在输入框或任一开关 / 按钮上时，Esc 都关闭查找条
        if (e.key !== "Escape") return;
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }}
    >
      <div className="relative w-80 shrink-0">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          ref={inputRef}
          value={query}
          aria-invalid={invalid || undefined}
          onChange={(e) => onQueryChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              // IME 合成中的 Enter 用于确认候选词
              if (e.nativeEvent.isComposing) return;
              e.preventDefault();
              if (e.shiftKey) onPrevious();
              else onNext();
            }
          }}
          placeholder={t("query.find.placeholder")}
          className="h-7 pl-7 pr-20 text-sm"
        />
        {statusText && (
          <span
            aria-live="polite"
            className={cn(
              "pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-xs tabular-nums",
              invalid ? "text-destructive" : "text-muted-foreground"
            )}
          >
            {statusText}
          </span>
        )}
      </div>
      {toggles.map(({ key, label, icon }) => (
        <Button
          key={key}
          variant="ghost"
          size="icon"
          className={cn("h-7 w-7", options[key] && "bg-accent text-accent-foreground")}
          aria-label={label}
          aria-pressed={options[key]}
          title={label}
          onClick={() => onOptionsChange({ ...options, [key]: !options[key] })}
        >
          {icon}
        </Button>
      ))}
      <div className="flex items-center border-l ml-0.5 pl-1">
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          aria-label={t("query.find.previous")}
          title={t("query.find.previous")}
          onClick={onPrevious}
        >
          <ChevronUp className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          aria-label={t("query.find.next")}
          title={t("query.find.next")}
          onClick={onNext}
        >
          <ChevronDown className="h-3.5 w-3.5" />
        </Button>
      </div>
      {scope && (
        <span className="ml-1 min-w-0 truncate text-xs text-muted-foreground">
          {t("query.find.scope", { rows: scope.rowCount.toLocaleString(), page: scope.page })}
        </span>
      )}
      <Button
        variant="ghost"
        size="icon"
        className="ml-auto h-7 w-7"
        aria-label={t("query.find.close")}
        title={t("query.find.close")}
        onClick={onClose}
      >
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}

/** 命中格内的显示文本：匹配片段用 warning 标记，当前命中用实色。 */
export function TableFindHighlight({
  text,
  ranges,
  current,
}: {
  text: string;
  ranges: readonly TableFindRange[];
  current: boolean;
}) {
  const parts: ReactNode[] = [];
  let cursor = 0;
  ranges.forEach(([start, end], i) => {
    if (start > cursor) parts.push(text.slice(cursor, start));
    parts.push(
      <mark
        key={i}
        data-find-current={current ? "" : undefined}
        className={cn(
          "rounded-[2px]",
          current ? "bg-warning text-warning-foreground" : "bg-warning/35 text-foreground"
        )}
      >
        {text.slice(start, end)}
      </mark>
    );
    cursor = end;
  });
  if (cursor < text.length) parts.push(text.slice(cursor));
  return <span className="truncate block">{parts}</span>;
}
