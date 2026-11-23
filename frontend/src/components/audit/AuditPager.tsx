import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from "lucide-react";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { AUDIT_PAGE_SIZES } from "./auditFilters";

interface AuditPagerProps {
  /** 从 0 开始 */
  page: number;
  pageSize: number;
  total: number;
  loading: boolean;
  onPage: (page: number) => void;
  onPageSize: (pageSize: number) => void;
}

/** 底栏：条数区间与总数、每页条数、首末页与页码跳转 */
export function AuditPager({ page, pageSize, total, loading, onPage, onPageSize }: AuditPagerProps) {
  const { t } = useTranslation();
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const [input, setInput] = useState(String(page + 1));
  const [prevPage, setPrevPage] = useState(page);
  if (prevPage !== page) {
    setPrevPage(page);
    setInput(String(page + 1));
  }
  const from = total === 0 ? 0 : page * pageSize + 1;
  const to = Math.min(total, (page + 1) * pageSize);
  const atFirst = page === 0;
  const atLast = page >= pages - 1;

  // 页码越界钳到首末页；输入不是数字时恢复当前页
  const commit = () => {
    const n = Number.parseInt(input, 10);
    if (Number.isNaN(n)) {
      setInput(String(page + 1));
      return;
    }
    const target = Math.min(pages, Math.max(1, n)) - 1;
    setInput(String(target + 1));
    if (target !== page) onPage(target);
  };

  return (
    <div className="h-10 px-4 border-t flex items-center gap-2 text-xs text-muted-foreground shrink-0">
      <span className="tabular-nums" data-testid="audit-range">
        {t("audit.range", { from: from.toLocaleString(), to: to.toLocaleString(), total: total.toLocaleString() })}
      </span>
      <div className="ml-auto flex items-center gap-1">
        <Select value={String(pageSize)} onValueChange={(v) => onPageSize(Number(v))}>
          <SelectTrigger className="h-7 w-auto whitespace-nowrap text-xs" aria-label={t("audit.pageSize")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {AUDIT_PAGE_SIZES.map((n) => (
              <SelectItem key={n} value={String(n)} className="text-xs">
                {t("audit.perPage", { count: n })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={atFirst || loading}
          onClick={() => onPage(0)}
          aria-label={t("audit.firstPage")}
        >
          <ChevronsLeft className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={atFirst || loading}
          onClick={() => onPage(page - 1)}
          aria-label={t("audit.prevPage")}
        >
          <ChevronLeft className="h-3.5 w-3.5" />
        </Button>
        <Input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === "Enter") commit();
          }}
          disabled={loading}
          aria-label={t("audit.pageNumber")}
          className="h-7 w-12 text-xs text-center px-1 tabular-nums"
        />
        <span className="tabular-nums">/ {pages.toLocaleString()}</span>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={atLast || loading}
          onClick={() => onPage(page + 1)}
          aria-label={t("audit.nextPage")}
        >
          <ChevronRight className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={atLast || loading}
          onClick={() => onPage(pages - 1)}
          aria-label={t("audit.lastPage")}
        >
          <ChevronsRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  );
}
