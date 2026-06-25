import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@opskat/ui";

export interface ConfigGroup {
  /** 稳定标识,用于激活态匹配与 data-testid。 */
  key: string;
  /** i18n key。 */
  label: string;
  /** 数量徽标(如 Connect 集群数);<=0 或 undefined 不显示。 */
  badge?: number;
  render: () => ReactNode;
}

interface ConfigTabsProps {
  groups: ConfigGroup[];
}

/** 资产表单类型配置的标签容器:多分组出"下划线坐于发丝线上"的标签,单分组退化为无标签单面板。 */
export function ConfigTabs({ groups }: ConfigTabsProps) {
  const { t } = useTranslation();
  const [active, setActive] = useState(groups[0]?.key ?? "");

  // 单分组:无标签,直接出内容。
  if (groups.length <= 1) {
    return <>{groups[0]?.render()}</>;
  }

  const activeGroup = groups.find((g) => g.key === active) ?? groups[0];

  return (
    <div className="w-full">
      <div role="tablist" className="flex items-end gap-6 border-b border-border">
        {groups.map((g) => {
          const isActive = g.key === activeGroup.key;
          return (
            <button
              key={g.key}
              type="button"
              role="tab"
              aria-selected={isActive}
              data-testid={`config-tab-${g.key}`}
              onClick={() => setActive(g.key)}
              className={cn(
                "relative flex items-center gap-1.5 pb-[9px] text-[13.5px] whitespace-nowrap transition-colors outline-none focus-visible:text-foreground",
                isActive ? "font-semibold text-primary" : "font-medium text-muted-foreground hover:text-foreground"
              )}
            >
              {t(g.label)}
              {g.badge !== undefined && g.badge > 0 && (
                <span className="rounded-full bg-primary/10 px-1.5 text-[10px] font-semibold text-primary">
                  {g.badge}
                </span>
              )}
              <span
                className={cn(
                  "absolute inset-x-0 bottom-[-1px] h-0.5 rounded-full",
                  isActive ? "bg-primary" : "bg-transparent"
                )}
              />
            </button>
          );
        })}
      </div>
      <div className="mt-5">{activeGroup.render()}</div>
    </div>
  );
}
