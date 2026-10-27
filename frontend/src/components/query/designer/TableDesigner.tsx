import { memo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Code2, Columns3, Link2, ListTree } from "lucide-react";
import { TabsContent } from "@opskat/ui";
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
import { DesignerFrame, DesignerStatus, type DesignerSubTab, type SubTabDef } from "./DesignerFrame";
import { TableDesignerEditor, type DesignerSave } from "./TableDesignerEditor";

export interface TableDesignerProps {
  tabId: string;
  innerTabId: string;
  database: string;
  table: string;
  /** view:只读查看;design:设计表(资产只读连接或驱动不能改表时仍只读)。 */
  mode: "view" | "design";
  /** 设计表的保存:草稿没有问题、驱动给出了语句时可点(任务 10 接上执行)。 */
  onSave?: (save: DesignerSave) => void;
}

/**
 * 表设计器内层标签。标签第一次到前台时读表结构,恢复的标签也是这样重新读取;能改表时读改表模型,
 * 否则读表元数据,两者映射成同一个 TableStructure。设计表时由 TableDesignerEditor 编辑草稿。
 */
export const TableDesigner = memo(function TableDesigner({
  tabId,
  innerTabId,
  database,
  table,
  mode,
  onSave,
}: TableDesignerProps) {
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
  const editable = mode === "design" && access.canAlter;
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

  const [subTab, setSubTab] = useState<DesignerSubTab>("fields");
  const structure = state.status === "ready" ? state.value : null;
  const showDDL = driverHasCapability(descriptor, "view_ddl");

  if (editable && structure) {
    return (
      <TableDesignerEditor
        tabId={tabId}
        assetId={assetId}
        database={database}
        table={table}
        descriptor={descriptor}
        structure={structure}
        subTab={subTab}
        onSubTabChange={setSubTab}
        onReload={reload}
        onSave={onSave}
      />
    );
  }

  const tabs: SubTabDef[] = [
    { id: "fields", label: t("query.designer.tab.fields"), icon: Columns3, count: structure?.columns.length },
    { id: "indexes", label: t("query.designer.tab.indexes"), icon: ListTree, count: structure?.indexes.length },
    {
      id: "foreignKeys",
      label: t("query.designer.tab.foreignKeys"),
      icon: Link2,
      count: structure?.foreignKeys.length,
    },
    // 设计表的最后一个子页是 SQL 预览,只读查看是建表语句(驱动声明查看 DDL 时)。
    ...(editable
      ? [{ id: "sql" as const, label: t("query.designer.tab.sql"), icon: Code2 }]
      : showDDL
        ? [{ id: "ddl" as const, label: t("query.designer.tab.ddl"), icon: Code2 }]
        : []),
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
    <DesignerFrame
      toolbar={
        <ReadOnlyToolbar
          tableName={structure?.name ?? table}
          comment={structure?.comment ?? ""}
          // 设计表还在读取时不标只读。
          readOnlyReason={editable ? undefined : access.readOnlyReason}
          loading={state.status === "loading"}
          onReload={retry}
        />
      }
      tabs={state.status === "error" ? null : tabs}
      subTab={subTab}
      onSubTabChange={setSubTab}
      status={
        <DesignerStatus
          driver={descriptor?.name}
          fullName={`${database}.${table}`}
          note={editable ? undefined : t("query.designer.readOnly.generic")}
        />
      }
    >
      {body}
    </DesignerFrame>
  );
});
