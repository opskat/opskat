import type { ComponentType, ReactNode } from "react";
import { cn } from "@opskat/ui";

/** 资产表单字段标签:11px、字距 0.3、弱化色,贴合 v3 设计稿。 */
export function FieldLabel({
  children,
  required,
  className,
  htmlFor,
}: {
  children: ReactNode;
  required?: boolean;
  className?: string;
  htmlFor?: string;
}) {
  return (
    <label
      htmlFor={htmlFor}
      className={cn("select-none text-[11px] font-medium tracking-[0.3px] text-muted-foreground", className)}
    >
      {children}
      {required && <span className="text-muted-foreground"> *</span>}
    </label>
  );
}

/** 标签 + 控件的竖直字段容器(label 与控件间距 7px)。 */
export function Field({
  label,
  required,
  className,
  children,
}: {
  label?: ReactNode;
  required?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex flex-col gap-[7px]", className)}>
      {label !== undefined && <FieldLabel required={required}>{label}</FieldLabel>}
      {children}
    </div>
  );
}

export interface SegmentedOption<T extends string> {
  value: T;
  label: ReactNode;
  /** lucide 风格图标组件,显示在文案左侧。 */
  icon?: ComponentType<{ className?: string }>;
  testid?: string;
  disabled?: boolean;
}

/** 分段控件:替代二选一 / 少选项的下拉。轨道为实心灰底,选中项为浮起的白色胶囊。 */
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  className,
  "aria-label": ariaLabel,
}: {
  value: T;
  onChange: (value: T) => void;
  options: SegmentedOption<T>[];
  className?: string;
  "aria-label"?: string;
}) {
  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      className={cn("flex h-9 w-full items-center gap-[3px] rounded-lg bg-muted p-[3px]", className)}
    >
      {options.map((opt) => {
        const active = opt.value === value;
        const Icon = opt.icon;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={opt.disabled}
            data-testid={opt.testid}
            data-state={active ? "active" : "inactive"}
            onClick={() => onChange(opt.value)}
            className={cn(
              "flex h-full flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent text-[13px] transition-colors outline-none focus-visible:ring-1 focus-visible:ring-ring/45 disabled:pointer-events-none disabled:opacity-50",
              active
                ? "bg-background font-semibold text-foreground shadow-sm dark:border-foreground/10 dark:bg-foreground/15 dark:shadow-none"
                : "font-medium text-muted-foreground hover:text-foreground"
            )}
          >
            {Icon && <Icon className="h-3.5 w-3.5" />}
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
