import { memo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Code2, Columns3, Link2, ListTree } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@opskat/ui";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { driverHasCapability, findDatabaseDriver, refreshDatabaseDrivers, useDatabaseDrivers } from "@/lib/dbDrivers";
import { useDesignerAccess } from "./access";
import { showsColumnComments, type TableStructure } from "./model";
import { useTableStructure, type LoadState } from "./useTableStructure";
import { ReadOnlyToolbar } from "./DesignerToolbar";
import { DesignerLoadError, DesignerSkeleton } from "./DesignerStates";
import { FieldsGrid } from "./FieldsGrid";
import { IndexList } from "./IndexList";
import { ForeignKeyList } from "./ForeignKeyList";
import { DdlPane } from "./DdlPane";

export interface TableDesignerProps {
  tabId: string;
  innerTabId: string;
  database: string;
  table: string;
}

type SubTab = "fields" | "indexes" | "foreignKeys" | "ddl";

/**
 * 表设计器内层标签(目前只有只读查看)。标签第一次到前台时读表结构,恢复的标签也是这样重新读取;
 * 能改表时读改表模型,否则读表元数据,两者映射成同一个 TableStructure。
 */
export const TableDesigner = memo(function TableDesigner({ tabId, innerTabId, database, table }: TableDesignerProps) {
  const { t } = useTranslation();
  const meta = useTabStore((s) => s.tabs.find((tab) => tab.id === tabId)?.meta as QueryTabMeta | undefined);
  const isOuterActive = useTabStore((s) => s.activeTabId === tabId);
  const isInnerActive = useQueryStore((s) => s.dbStates[tabId]?.activeInnerTabId === innerTabId);
  const closeInnerTab = useQueryStore((s) => s.closeInnerTab);
  const assetId = meta?.assetId ?? 0;

  const driversState = useDatabaseDrivers();
  const descriptor =
    driversState.status === "ready" ? findDatabaseDriver(driversState.drivers, meta?.driver ?? "") : undefined;
  const access = useDesignerAccess(descriptor, assetId);
  const source = descriptor ? (access.canAlter ? "alterModel" : "metadata") : null;
  const { state: structureState, reload } = useTableStructure({
    assetId,
    database,
    table,
    source,
    enabled: isOuterActive && isInnerActive,
  });
  // 驱动描述读取失败时,设计器以同样的失败界面给出原因,重试即重新读描述。
  const state: LoadState<TableStructure> =
    driversState.status === "error" ? { status: "error", error: driversState.error } : structureState;
  const retry = driversState.status === "error" ? refreshDatabaseDrivers : reload;

  const [subTab, setSubTab] = useState<SubTab>("fields");
  const structure = state.status === "ready" ? state.value : null;
  const showDDL = driverHasCapability(descriptor, "view_ddl");

  const tabs: { id: SubTab; label: string; icon: typeof Columns3; count?: number }[] = [
    { id: "fields", label: t("query.designer.tab.fields"), icon: Columns3, count: structure?.columns.length },
    { id: "indexes", label: t("query.designer.tab.indexes"), icon: ListTree, count: structure?.indexes.length },
    {
      id: "foreignKeys",
      label: t("query.designer.tab.foreignKeys"),
      icon: Link2,
      count: structure?.foreignKeys.length,
    },
    ...(showDDL ? [{ id: "ddl" as const, label: t("query.designer.tab.ddl"), icon: Code2 }] : []),
  ];

  let body: ReactNode;
  if (state.status === "error") {
    body = <DesignerLoadError error={state.error} onRetry={retry} onClose={() => closeInnerTab(tabId, innerTabId)} />;
  } else if (!structure) {
    body = <DesignerSkeleton />;
  } else {
    body = (
      <>
        <TabsContent value="fields" className="flex min-h-0 flex-col">
          <FieldsGrid columns={structure.columns} showComment={showsColumnComments(descriptor, structure.columns)} />
        </TabsContent>
        <TabsContent value="indexes" className="flex min-h-0 flex-col">
          <IndexList
            indexes={structure.indexes}
            descriptor={descriptor}
            // 能改表、但驱动没声明索引编辑(改表模型不带索引):索引只能查看。
            notEditableNotice={source === "alterModel" && !structure.indexesEditable}
          />
        </TabsContent>
        <TabsContent value="foreignKeys" className="flex min-h-0 flex-col">
          <ForeignKeyList foreignKeys={structure.foreignKeys} />
        </TabsContent>
        {showDDL && (
          <TabsContent value="ddl" className="flex min-h-0 flex-col">
            <DdlPane assetId={assetId} database={database} table={table} />
          </TabsContent>
        )}
      </>
    );
  }

  return (
    <div className="flex h-full flex-col bg-background text-foreground">
      <ReadOnlyToolbar
        tableName={structure?.name ?? table}
        comment={structure?.comment ?? ""}
        readOnlyReason={access.readOnlyReason}
        loading={state.status === "loading"}
        onReload={retry}
      />
      <Tabs value={subTab} onValueChange={(value) => setSubTab(value as SubTab)} className="min-h-0 flex-1 gap-0">
        {state.status !== "error" && (
          <TabsList
            variant="line"
            className="h-9 w-full shrink-0 justify-start border-b border-border bg-muted/20 px-2"
          >
            {tabs.map(({ id, label, icon: Icon, count }) => (
              <TabsTrigger
                key={id}
                value={id}
                // 下划线贴着子页条底边,用主色标出当前子页。
                className="h-full flex-none gap-1.5 rounded-none px-2.5 text-xs font-normal after:bg-primary group-data-[orientation=horizontal]/tabs:after:bottom-0"
              >
                <Icon className="h-3.5 w-3.5" aria-hidden />
                {label}
                {count !== undefined && (
                  <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground tabular-nums">{count}</span>
                )}
              </TabsTrigger>
            ))}
          </TabsList>
        )}
        {body}
      </Tabs>
      <div
        data-testid="designer-status"
        className="flex h-6 shrink-0 items-center gap-1.5 border-t border-border bg-muted/30 px-3 text-[11px] text-muted-foreground"
      >
        {descriptor && <span>{descriptor.name}</span>}
        {descriptor && <span aria-hidden>·</span>}
        <span className="select-text">{`${database}.${table}`}</span>
        <span aria-hidden>·</span>
        <span>{t("query.designer.readOnly.generic")}</span>
      </div>
    </div>
  );
});
