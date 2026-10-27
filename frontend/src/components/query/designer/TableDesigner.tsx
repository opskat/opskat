import { memo, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Code2, Columns3, Link2, ListTree } from "lucide-react";
import { TabsContent } from "@opskat/ui";
import { designerTabId, useQueryStore } from "@/stores/queryStore";
import { notifySuccess } from "@/lib/notify";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { driverHasCapability, findDatabaseDriver, refreshDatabaseDrivers, useDatabaseDrivers } from "@/lib/dbDrivers";
import { useDesignerAccess } from "./access";
import { showsColumnComments, type TableStructure } from "./model";
import { useTableStructure, type LoadState } from "./useTableStructure";
import { ReadOnlyToolbar } from "./DesignerToolbar";
import { DesignerLoadError, DesignerSkeleton } from "./DesignerStates";
import { FieldsGrid } from "./FieldsGrid";
import { IndexList } from "./IndexList";
import { UpdateDriverAction } from "./UpdateDriverAction";
import { ForeignKeyList } from "./ForeignKeyList";
import { DdlPane } from "./DdlPane";
import { DesignerFrame, DesignerStatus, type DesignerSubTab, type SubTabDef } from "./DesignerFrame";
import { TableDesignerEditor, type DesignerSave } from "./TableDesignerEditor";
import type { TableDraft } from "./draft";
import { useSaveFlow, type SaveOutcome } from "./useSaveFlow";
import { SaveDialog } from "./SaveDialog";
import { dropHandoff, handOver, peekHandoff, type CloseAfterSave, type DesignerSavePlan } from "./handoff";

export interface TableDesignerProps {
  tabId: string;
  innerTabId: string;
  database: string;
  table: string;
  /** view:只读查看;design:设计表(资产只读连接或驱动不能改表时仍只读)。 */
  mode: "view" | "design";
}

/**
 * 表设计器内层标签。标签第一次到前台时读表结构,恢复的标签也是这样重新读取;能改表时读改表模型,
 * 否则读表元数据,两者映射成同一个 TableStructure。设计表时由 TableDesignerEditor 编辑草稿,保存
 * 经 useSaveFlow 确认并执行:全部成功后重新读取(改了表名时标签换成新名字),停在中途时重新读取并把
 * 没执行的改动留在草稿里。
 */
export const TableDesigner = memo(function TableDesigner({
  tabId,
  innerTabId,
  database,
  table,
  mode,
}: TableDesignerProps) {
  const { t } = useTranslation();
  const meta = useTabStore((s) => s.tabs.find((tab) => tab.id === tabId)?.meta as QueryTabMeta | undefined);
  const isOuterActive = useTabStore((s) => s.activeTabId === tabId);
  const isInnerActive = useQueryStore((s) => s.dbStates[tabId]?.activeInnerTabId === innerTabId);
  const closeInnerTab = useQueryStore((s) => s.closeInnerTab);
  const refreshTables = useQueryStore((s) => s.refreshTables);
  const renameDesignerTab = useQueryStore((s) => s.renameDesignerTab);
  const forceCloseInnerTabs = useQueryStore((s) => s.forceCloseInnerTabs);
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

  // 改表名已生效、停在中途的保存:上一个名字的设计器把结果视图与草稿交给这里。
  const [handoff] = useState(() => peekHandoff(tabId, innerTabId));
  useEffect(() => dropHandoff(tabId, innerTabId), [tabId, innerTabId]);
  const [subTab, setSubTab] = useState<DesignerSubTab>(handoff?.subTab ?? "fields");
  // 停在中途后重新读取时,编辑器以这份草稿为起点(搬到读出的表上)。
  const [rebaseFrom, setRebaseFrom] = useState<TableDraft | null>(handoff?.flow.plan.save.draft ?? null);

  const afterSave = async (plan: DesignerSavePlan, outcome: SaveOutcome<DesignerSavePlan>) => {
    if (outcome.kind === "success") {
      notifySuccess(t("query.designer.save.done", { name: `${database}.${plan.save.table}` }));
      // 连外层查询标签一起关时树也不在了,不必刷新。
      if (!plan.close?.closeOuter) void refreshTables(tabId, database);
      setRebaseFrom(null);
      if (plan.close) forceCloseInnerTabs(tabId, plan.close.ids, plan.close.closeOuter);
      else if (plan.save.table !== table) renameDesignerTab(tabId, innerTabId, plan.save.table);
      else reload();
      return;
    }
    // 停在中途:先刷新树的表列表,据此判断改表名那一条有没有生效。
    await refreshTables(tabId, database);
    const tables = useQueryStore.getState().dbStates[tabId]?.tables[database];
    const renamed = plan.save.table !== table && tables?.includes(plan.save.table) && !tables.includes(table);
    if (renamed) {
      handOver(tabId, designerTabId(database, plan.save.table), { flow: outcome.state, subTab });
      renameDesignerTab(tabId, innerTabId, plan.save.table);
      return;
    }
    setRebaseFrom(plan.save.draft);
    reload();
  };
  const flow = useSaveFlow<DesignerSavePlan>({
    assetId,
    database,
    initial: handoff?.flow,
    onExecuted: (plan, outcome) => void afterSave(plan, outcome),
  });
  const startSave = (save: DesignerSave, close?: CloseAfterSave) =>
    flow.open({
      title: t("query.designer.save.confirmTitle", { name: `${database}.${table}` }),
      statements: save.statements,
      droppedColumns: save.droppedColumns,
      droppedIndexes: save.droppedIndexes,
      save,
      close,
    });
  const saveDialog = (
    <SaveDialog
      state={flow.state}
      transactional={!!descriptor?.transactionalDDL}
      onConfirm={() => void flow.confirm()}
      onClose={flow.close}
    />
  );

  const structure = state.status === "ready" ? state.value : null;
  const showDDL = driverHasCapability(descriptor, "view_ddl");

  if (editable && structure) {
    return (
      <>
        <TableDesignerEditor
          tabId={tabId}
          innerTabId={innerTabId}
          active={isOuterActive && isInnerActive}
          assetId={assetId}
          database={database}
          table={table}
          descriptor={descriptor}
          structure={structure}
          subTab={subTab}
          onSubTabChange={setSubTab}
          onReload={() => {
            setRebaseFrom(null);
            reload();
          }}
          rebaseFrom={rebaseFrom}
          saving={flow.state.phase !== "idle"}
          onSave={startSave}
        />
        {saveDialog}
      </>
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
            // 能改表、但驱动没声明索引编辑(改表模型不带索引):索引只能查看,扩展驱动给出「去更新」。
            notEditableNotice={source === "alterModel" && !structure.indexesEditable}
            updateDriverAction={<UpdateDriverAction descriptor={descriptor} />}
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
    <>
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
      {saveDialog}
    </>
  );
});
