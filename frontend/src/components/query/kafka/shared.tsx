import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, RotateCw } from "lucide-react";
import { Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";

/**
 * 「暂无…」占位。`failed` 为真（这次加载失败，原因与重试在页头）时不渲染，
 * 只有请求成功且结果为空才显示空态。
 */
export function EmptyState({ text, failed = false }: { text: string; failed?: boolean }) {
  if (failed) return null;
  return <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">{text}</div>;
}

export function LoadingBlock() {
  return (
    <div className="flex h-32 items-center justify-center">
      <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  );
}

export function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-md border bg-background px-3 py-2">
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums">{value}</div>
    </div>
  );
}

export function StatusPill({ value }: { value?: string }) {
  if (!value) return <span className="text-muted-foreground">-</span>;
  return (
    <span className="inline-flex rounded border bg-muted px-1.5 py-0.5 text-xs font-medium uppercase text-muted-foreground">
      {value}
    </span>
  );
}

export function Info({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className={`mt-1 truncate ${mono ? "font-mono text-xs" : ""}`}>{value}</div>
    </div>
  );
}

export function CompactSelect({
  value,
  onChange,
  items,
}: {
  value: string;
  onChange: (value: string) => void;
  items: string[];
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="h-8 text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((item) => (
          <SelectItem key={item} value={item}>
            {item}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/** 失败的完整原因：就地换行展示，不截断；传入 onRetry 时附「重试」。 */
export function ErrorNotice({
  message,
  onRetry,
  className = "",
}: {
  message?: string | null;
  onRetry?: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  if (!message) return null;
  return (
    <div
      role="alert"
      className={`flex items-start gap-1.5 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive ${className}`}
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">{message}</span>
      {onRetry && (
        <Button size="sm" variant="outline" className="h-6 shrink-0 gap-1 px-2 text-xs" onClick={onRetry}>
          <RotateCw className="h-3 w-3" />
          {t("action.retry")}
        </Button>
      )}
    </div>
  );
}
