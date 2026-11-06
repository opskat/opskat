import type { ReactNode, Ref } from "react";
import type { LucideIcon } from "lucide-react";
import { Tabs, TabsList, TabsTrigger } from "@opskat/ui";

export type DesignerSubTab = "fields" | "indexes" | "foreignKeys" | "ddl" | "sql";

export interface SubTabDef {
  id: DesignerSubTab;
  label: string;
  icon: LucideIcon;
  count?: number;
  /** 子页里有未保存的改动:标签上显示圆点。 */
  dirty?: boolean;
}

interface DesignerFrameProps {
  rootRef?: Ref<HTMLDivElement>;
  toolbar: ReactNode;
  /** 子页标签;读取失败时为 null(不显示子页条)。 */
  tabs: SubTabDef[] | null;
  subTab: DesignerSubTab;
  onSubTabChange: (tab: DesignerSubTab) => void;
  /** 子页条右侧(如「添加字段」)。 */
  tabsRight?: ReactNode;
  /** 子页内容(TabsContent)或整块的加载 / 失败界面。 */
  children: ReactNode;
  /** 状态栏上方(问题与提醒)。 */
  footer?: ReactNode;
  /** 状态栏的内容。 */
  status: ReactNode;
}

/** 状态栏:驱动名与表的全名,只读时再标一句。 */
export function DesignerStatus({ driver, fullName, note }: { driver?: string; fullName: string; note?: string }) {
  return (
    <>
      {driver && <span>{driver}</span>}
      {driver && <span aria-hidden>·</span>}
      <span className="select-text">{fullName}</span>
      {note && <span aria-hidden>·</span>}
      {note && <span>{note}</span>}
    </>
  );
}

/** 设计器的外壳:工具栏、子页条、内容、底部问题区与状态栏;只读查看与设计表共用。 */
export function DesignerFrame({
  rootRef,
  toolbar,
  tabs,
  subTab,
  onSubTabChange,
  tabsRight,
  children,
  footer,
  status,
}: DesignerFrameProps) {
  return (
    <div ref={rootRef} className="flex h-full flex-col bg-background text-foreground">
      {toolbar}
      <Tabs
        value={subTab}
        onValueChange={(value) => onSubTabChange(value as DesignerSubTab)}
        className="min-h-0 flex-1 gap-0"
      >
        {tabs && (
          <div className="flex h-9 w-full shrink-0 items-center border-b border-border bg-muted/20 px-2">
            <TabsList variant="line" className="h-full justify-start bg-transparent p-0">
              {tabs.map(({ id, label, icon: Icon, count, dirty }) => (
                <TabsTrigger
                  key={id}
                  value={id}
                  // 下划线贴着子页条底边,用主色标出当前子页。
                  className="h-full flex-none gap-1.5 rounded-none px-2.5 text-xs font-normal after:bg-primary group-data-[orientation=horizontal]/tabs:after:bottom-0"
                >
                  <Icon className="h-3.5 w-3.5" aria-hidden />
                  {label}
                  {count !== undefined && (
                    <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground tabular-nums">
                      {count}
                    </span>
                  )}
                  {dirty && <span className="size-1.5 rounded-full bg-warning" aria-hidden />}
                </TabsTrigger>
              ))}
            </TabsList>
            {tabsRight && <div className="ml-auto flex items-center">{tabsRight}</div>}
          </div>
        )}
        {children}
      </Tabs>
      {footer}
      <div
        data-testid="designer-status"
        className="flex h-6 shrink-0 items-center gap-1.5 border-t border-border bg-muted/30 px-3 text-[11px] text-muted-foreground"
      >
        {status}
      </div>
    </div>
  );
}
