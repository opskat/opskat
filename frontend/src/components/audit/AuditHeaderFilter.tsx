import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Check, Filter } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger, cn } from "@opskat/ui";

interface AuditHeaderFilterProps {
  /** 列名，也用于按钮的可读标签 */
  label: string;
  testId: string;
  active: boolean;
  /** 列头只留筛选图标（窄列） */
  hideLabel?: boolean;
  align?: "start" | "end";
  className?: string;
  contentClassName?: string;
  children: (close: () => void) => ReactNode;
}

/** 列头 + 漏斗按钮；点开弹出该列的筛选项 */
export function AuditHeaderFilter({
  label,
  testId,
  active,
  hideLabel,
  align = "start",
  className,
  contentClassName,
  children,
}: AuditHeaderFilterProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <th className={cn("px-4 py-2 font-medium", className)} title={hideLabel ? label : undefined}>
      <div className="inline-flex items-center gap-1">
        {!hideLabel && <span className={cn(active && "text-foreground")}>{label}</span>}
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <button
              type="button"
              data-testid={testId}
              aria-label={t("audit.filterBy", { column: label })}
              aria-pressed={active}
              className={cn(
                "h-5 w-5 inline-flex items-center justify-center rounded hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                active ? "text-primary" : "text-muted-foreground/60 hover:text-foreground"
              )}
            >
              <Filter className={cn("h-3 w-3", active && "fill-current")} />
            </button>
          </PopoverTrigger>
          <PopoverContent align={align} className={cn("w-44 p-1.5 text-xs font-normal", contentClassName)}>
            {children(() => setOpen(false))}
          </PopoverContent>
        </Popover>
      </div>
    </th>
  );
}

export interface AuditOption {
  value: string;
  label: string;
}

/** 单选列表；第一项为「全部」（空值） */
export function AuditOptionList({
  value,
  options,
  onPick,
  mono,
}: {
  value: string;
  options: AuditOption[];
  onPick: (value: string) => void;
  mono?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col max-h-72 overflow-y-auto">
      {[{ value: "", label: t("audit.all") }, ...options].map((o) => (
        <button
          key={o.value || "__all"}
          type="button"
          aria-pressed={value === o.value}
          onClick={() => onPick(o.value)}
          className={cn(
            "flex items-center justify-between gap-2 h-7 px-2 rounded text-left hover:bg-accent focus-visible:outline-none focus-visible:bg-accent",
            mono && o.value && "font-mono"
          )}
        >
          <span className="truncate">{o.label}</span>
          {value === o.value && <Check className="h-3.5 w-3.5 shrink-0 text-primary" />}
        </button>
      ))}
    </div>
  );
}

/** 「全部 / A / B」分段按钮 */
export function AuditSegmented({
  value,
  options,
  onChange,
}: {
  value: string;
  options: AuditOption[];
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="inline-flex rounded-md border p-0.5 bg-muted/40">
      {[{ value: "", label: t("audit.all") }, ...options].map((o) => (
        <button
          key={o.value || "__all"}
          type="button"
          aria-pressed={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "h-6 px-2.5 text-xs rounded-[5px] transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
            value === o.value
              ? "bg-background shadow-sm text-foreground"
              : "text-muted-foreground hover:text-foreground"
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
