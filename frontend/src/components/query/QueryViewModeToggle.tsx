import { useTranslation } from "react-i18next";
import { Braces, Table2 } from "lucide-react";
import { cn } from "@opskat/ui";

export type QueryViewMode = "table" | "json";

/**
 * 结果区的表格 / JSON 切换。视觉沿用设计系统 Segmented 的习惯(实心灰轨道 + 浮起胶囊),
 * 但那个组件是 h-9 w-full 的表单控件,放不进结果区工具栏,所以按工具栏高度单做一份。
 */
export function QueryViewModeToggle({
  value,
  onChange,
  className,
}: {
  value: QueryViewMode;
  onChange: (value: QueryViewMode) => void;
  className?: string;
}) {
  const { t } = useTranslation();
  const options = [
    { value: "table" as const, label: t("query.tableView"), icon: Table2 },
    { value: "json" as const, label: t("query.jsonView"), icon: Braces },
  ];

  return (
    <div
      role="radiogroup"
      aria-label={t("query.resultView")}
      className={cn("flex h-6 shrink-0 items-center gap-[2px] rounded-md bg-muted p-[2px]", className)}
    >
      {options.map((option) => {
        const active = option.value === value;
        const Icon = option.icon;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(option.value)}
            className={cn(
              "flex h-full items-center gap-1 rounded-[5px] border px-2 text-xs transition-colors outline-none focus-visible:ring-1 focus-visible:ring-ring/45",
              active
                ? "border-border/60 bg-background text-foreground shadow-sm"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            <Icon className="h-3.5 w-3.5" />
            {option.label}
          </button>
        );
      })}
    </div>
  );
}
