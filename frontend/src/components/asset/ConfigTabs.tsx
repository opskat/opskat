import { forwardRef, useImperativeHandle, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@opskat/ui";

export interface ConfigGroup {
  /** 稳定标识,用于 focusGroup 跳转。 */
  key: string;
  /** i18n key。 */
  label: string;
  /** true → 标签显示"可选"标(第一个「连接」默认 false)。 */
  optional?: boolean;
  /** 数量徽标(如 Connect 集群数);<=0 或 undefined 不显示。 */
  badge?: number;
  /** 红点:该分组"已启用但必填没填全"。 */
  invalid?: boolean;
  render: () => ReactNode;
}

export interface ConfigTabsHandle {
  setActive: (key: string) => void;
}

interface ConfigTabsProps {
  groups: ConfigGroup[];
}

/** 资产表单类型配置的标签容器:多分组出顶部标签,单分组退化为无标签单面板。 */
export const ConfigTabs = forwardRef<ConfigTabsHandle, ConfigTabsProps>(function ConfigTabs({ groups }, ref) {
  const { t } = useTranslation();
  const [active, setActive] = useState(groups[0]?.key ?? "");

  useImperativeHandle(ref, () => ({ setActive }), []);

  // 单分组:无标签,直接出内容。
  if (groups.length <= 1) {
    return <>{groups[0]?.render()}</>;
  }

  return (
    <Tabs value={active} onValueChange={setActive} className="w-full">
      <TabsList className="flex w-full justify-start overflow-x-auto">
        {groups.map((g) => (
          <TabsTrigger key={g.key} value={g.key} data-testid={`config-tab-${g.key}`} className="gap-1.5">
            {t(g.label)}
            {g.badge !== undefined && g.badge > 0 && (
              <span className="rounded-full bg-primary/10 px-1.5 text-[10px] font-semibold text-primary">
                {g.badge}
              </span>
            )}
            {g.optional && <span className="text-[10px] text-muted-foreground">{t("asset.optional")}</span>}
            {g.invalid && (
              <span
                data-testid={`config-tab-dot-${g.key}`}
                className="h-1.5 w-1.5 rounded-full bg-destructive ring-2 ring-destructive/20"
              />
            )}
          </TabsTrigger>
        ))}
      </TabsList>
      {groups.map((g) => (
        <TabsContent key={g.key} value={g.key} className="mt-3 space-y-3">
          {g.render()}
        </TabsContent>
      ))}
    </Tabs>
  );
});
