import type { ComponentType, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@opskat/ui";
import { useShownError } from "@/components/asset/useConfigSection";

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

/** 标签 + 控件的竖直字段容器(label 与控件间距 7px)。error 画在控件下;控件自己负责 aria-invalid。 */
export function Field({
  label,
  required,
  error,
  className,
  children,
}: {
  label?: ReactNode;
  required?: boolean;
  error?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex flex-col gap-[7px]", className)}>
      {label !== undefined && <FieldLabel required={required}>{label}</FieldLabel>}
      {children}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

/**
 * 按资产表单的保存约定校验的字段:error 是该字段当前的错误(i18n key,空串 = 无错),只在保存被拒后
 * 画在控件下并向壳报告;children 拿到 invalid 给控件设 aria-invalid。声明式字段走 FieldDesc.error,
 * 这个给区块里手写的控件用。
 */
export function CheckedField({
  error,
  children,
  ...field
}: {
  label?: ReactNode;
  required?: boolean;
  className?: string;
  error: string;
  children: (invalid: boolean) => ReactNode;
}) {
  const { t } = useTranslation();
  const invalid = useShownError(error !== "");
  return (
    <Field {...field} error={invalid ? t(error) : undefined}>
      {children(invalid)}
    </Field>
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
