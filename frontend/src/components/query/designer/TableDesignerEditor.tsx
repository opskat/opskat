import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Code2, Columns3, Link2, ListTree, Plus } from "lucide-react";
import { Button, ConfirmDialog, TabsContent } from "@opskat/ui";
import type { dbdriver } from "../../../../wailsjs/go/models";
import { useQueryStore } from "@/stores/queryStore";
import { eventMatchesBinding, formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { driverCanAlter, type DriverDescriptor } from "@/lib/dbDrivers";
import { showsColumnComments, showsIndexComments, type TableStructure } from "./model";
import { droppedColumns, tableCommentChanged, tableNameChanged, type DraftIssue, type TableDraft } from "./draft";
import {
  droppedIndexes as draftDroppedIndexes,
  indexDefinition,
  indexDeleted,
  primaryKeyColumnKeys,
} from "./indexDraft";
import { useTableDraft } from "./useTableDraft";
import { useAlterPreview } from "./useAlterPreview";
import { issueMessage } from "./issueText";
import { DesignToolbar } from "./DesignerToolbar";
import { DesignerFrame, DesignerStatus, type DesignerSubTab, type SubTabDef } from "./DesignerFrame";
import { EditableFieldsGrid } from "./FieldsGrid";
import { ColumnProperties } from "./ColumnProperties";
import { CapabilityBar, IssueList } from "./DesignerNotices";
import { IndexList } from "./IndexList";
import { EditableIndexList } from "./EditableIndexList";
import { IndexEditor } from "./IndexEditor";
import { UpdateDriverAction } from "./UpdateDriverAction";
import { ForeignKeyList } from "./ForeignKeyList";
import { SqlPreviewPane } from "./SqlPreviewPane";
import { CloseGuardDialog } from "./CloseGuardDialog";
import type { CloseAfterSave } from "./handoff";

/**
 * 保存时交给调用方的内容:驱动按当前草稿生成的语句(就是要执行的),请求本身,改表后的表名,草稿(停在
 * 中途时搬到重新读出的表上),与草稿里删除的已有列和随之删除的索引(确认框的警告)。
 */
export interface DesignerSave {
  statements: string[];
  request: dbdriver.AlterTableRequest;
  /** 改表后的表名(列表给出的形式);改了名时与打开时不同。 */
  table: string;
  draft: TableDraft;
  droppedColumns: string[];
  droppedIndexes: string[];
}

interface TableDesignerEditorProps {
  tabId: string;
  innerTabId: string;
  /** 这个设计器标签在前台(外层查询标签与内层标签都是当前的):保存快捷键只在这时响应。 */
  active: boolean;
  assetId: number;
  database: string;
  table: string;
  descriptor: DriverDescriptor | undefined;
  structure: TableStructure;
  subTab: DesignerSubTab;
  onSubTabChange: (tab: DesignerSubTab) => void;
  /** 重新读取表结构(草稿随之丢弃)。 */
  onReload: () => void;
  /** 保存停在中途后重新读取时的草稿:搬到读出的表上作为起点。 */
  rebaseFrom: TableDraft | null;
  /** 保存的确认或结果正在显示:快捷键与关闭拦截让路。 */
  saving: boolean;
  /** 保存:打开确认。close 是关闭拦截里点「保存…」时挂着的关闭,保存全部成功后照此关闭。 */
  onSave: (save: DesignerSave, close?: CloseAfterSave) => void;
}

type PendingConfirm = "revertAll" | "reload" | null;

/** 聚焦格子时最多再等几帧(切子页后内容挂载)。 */
const FOCUS_RETRY_FRAMES = 10;

/**
 * 设计表:字段直接编辑、改动标记与还原、按驱动声明锁定、校验与提醒;SQL 预览随草稿重新生成,
 * 驱动报错或草稿有问题时保存不可用。
 */
export function TableDesignerEditor({
  tabId,
  innerTabId,
  active,
  assetId,
  database,
  table,
  descriptor,
  structure,
  subTab,
  onSubTabChange,
  onReload,
  rebaseFrom,
  saving,
  onSave,
}: TableDesignerEditorProps) {
  const { t } = useTranslation();
  const openSqlTab = useQueryStore((s) => s.openSqlTab);
  const draftState = useTableDraft(structure, descriptor, rebaseFrom);
  const { draft, counts, indexCounts, isDirty, issues, request } = draftState;
  const errors = issues.filter((i) => i.severity === "error");
  const preview = useAlterPreview({ assetId, database, table, request, enabled: errors.length === 0 });
  // 索引读自改表模型(驱动声明了索引编辑)时才能改;否则只读并说明。
  const indexesEditable = structure.indexesEditable && !!descriptor?.indexEditing;

  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [selectedIndexKey, setSelectedIndexKey] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<PendingConfirm>(null);
  const selected = draft.columns.find((c) => c.key === selectedKey && !c.deleted);
  const selectedIndex = indexesEditable ? draft.indexes.find((i) => i.key === selectedIndexKey) : undefined;

  // 问题列表、添加字段要聚焦的格子:由下面的 effect 聚焦。切子页时字段页的内容要晚一两帧才挂载
  // (Radix 的 Presence),找不到就在后面几帧再找。
  const rootRef = useRef<HTMLDivElement>(null);
  const [focusRequest, setFocusRequest] = useState<{ cell: string } | null>(null);
  useEffect(() => {
    if (!focusRequest) return;
    let frame = 0;
    let attempts = 0;
    const tryFocus = () => {
      const cells = rootRef.current?.querySelectorAll<HTMLElement>("[data-cell]") ?? [];
      const target = Array.from(cells).find((cell) => cell.dataset.cell === focusRequest.cell);
      if (target) target.focus();
      else if (attempts++ < FOCUS_RETRY_FRAMES) frame = requestAnimationFrame(tryFocus);
    };
    tryFocus();
    return () => cancelAnimationFrame(frame);
  }, [focusRequest]);

  const focusCell = (cell: string) => setFocusRequest({ cell });
  const selectIssue = (issue: DraftIssue) => {
    if (issue.target.kind === "tableName") {
      focusCell("table:name");
      return;
    }
    if (issue.target.kind === "index") {
      onSubTabChange("indexes");
      setSelectedIndexKey(issue.target.key);
      focusCell(`${issue.target.key}:${issue.target.field}`);
      return;
    }
    onSubTabChange("fields");
    if (issue.target.kind === "column") {
      setSelectedKey(issue.target.key);
      focusCell(`${issue.target.key}:${issue.target.field}`);
    }
  };

  const addField = () => {
    const key = draftState.addColumn();
    setSelectedKey(key);
    focusCell(`${key}:name`);
  };

  const addIndex = () => {
    const key = draftState.addIndex();
    setSelectedIndexKey(key);
    focusCell(`${key}:name`);
  };

  const confirmOrRun = (kind: Exclude<PendingConfirm, null>) => {
    if (isDirty) {
      setConfirm(kind);
      return;
    }
    if (kind === "reload") onReload();
  };
  const runConfirmed = () => {
    if (confirm === "revertAll") {
      draftState.revertAll();
      setSelectedKey(null);
      setSelectedIndexKey(null);
    } else if (confirm === "reload") {
      onReload();
    }
    setConfirm(null);
  };

  const dropped = useMemo(() => droppedColumns(draft), [draft]);
  const droppedIndexes = useMemo(() => draftDroppedIndexes(draft), [draft]);
  const primaryKeys = useMemo(
    () => (indexesEditable ? primaryKeyColumnKeys(draft) : undefined),
    [draft, indexesEditable]
  );
  const indexDefinitions = useMemo(() => draft.indexes.map((i) => indexDefinition(draft, i)), [draft]);

  const statements = preview.status === "ready" ? preview.statements : [];
  const canSave = errors.length === 0 && preview.status === "ready" && statements.length > 0;
  const save = (close?: CloseAfterSave) => {
    if (!canSave || preview.status !== "ready") return;
    onSave(
      {
        statements,
        request,
        table: preview.table,
        draft,
        droppedColumns: dropped,
        droppedIndexes,
      },
      close
    );
  };

  // 有无改动上报给查询 store:关闭本标签、批量关闭、关闭外层查询标签据此拦下来问。
  const setInnerTabDirty = useQueryStore((s) => s.setInnerTabDirty);
  useEffect(() => {
    setInnerTabDirty(tabId, innerTabId, isDirty);
    return () => setInnerTabDirty(tabId, innerTabId, false);
  }, [innerTabId, isDirty, setInnerTabDirty, tabId]);

  // 关闭请求只涉及本标签这一个有改动的标签时,由这里的拦截决定(多个时 DatabasePanel 弹批量框)。
  const closeRequest = useQueryStore((s) => s.dbStates[tabId]?.closeRequest);
  const forceCloseInnerTabs = useQueryStore((s) => s.forceCloseInnerTabs);
  const cancelCloseRequest = useQueryStore((s) => s.cancelCloseRequest);
  const guarding = !saving && closeRequest?.dirtyIds.length === 1 && closeRequest.dirtyIds[0] === innerTabId;
  const changeCount =
    counts.added +
    counts.modified +
    counts.deleted +
    indexCounts.added +
    indexCounts.modified +
    indexCounts.deleted +
    (tableNameChanged(draft) ? 1 : 0) +
    (tableCommentChanged(draft) ? 1 : 0);

  // 保存快捷键(快捷键设置里可改):只在本标签在前台、能保存、没有对话框时响应,其余时候不拦下按键。
  const saveBinding = useShortcutStore((s) => s.shortcuts["designer.save"]);
  const onShortcut = useEffectEvent((e: KeyboardEvent) => {
    if (!active || !canSave || saving || guarding || confirm !== null) return;
    const { shortcuts, isRecording } = useShortcutStore.getState();
    if (isRecording || !eventMatchesBinding(e, shortcuts["designer.save"])) return;
    e.preventDefault();
    save();
  });
  useEffect(() => {
    const handler = (e: KeyboardEvent) => onShortcut(e);
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);
  const tableNameIssue = (severity: DraftIssue["severity"]) =>
    issues.find((i) => i.target.kind === "tableName" && i.severity === severity);
  const tableNameWarning = tableNameIssue("warning");
  const liveColumns = draft.columns.filter((c) => !c.deleted).length;

  const tabs: SubTabDef[] = [
    {
      id: "fields",
      label: t("query.designer.tab.fields"),
      icon: Columns3,
      count: liveColumns,
      dirty: counts.added + counts.modified + counts.deleted > 0,
    },
    {
      id: "indexes",
      label: t("query.designer.tab.indexes"),
      icon: ListTree,
      count: draft.indexes.filter((i) => !indexDeleted(draft, i)).length,
      dirty: indexCounts.added + indexCounts.modified + indexCounts.deleted > 0,
    },
    {
      id: "foreignKeys",
      label: t("query.designer.tab.foreignKeys"),
      icon: Link2,
      count: structure.foreignKeys.length,
    },
    { id: "sql", label: t("query.designer.tab.sql"), icon: Code2 },
  ];

  return (
    <DesignerFrame
      rootRef={rootRef}
      toolbar={
        <DesignToolbar
          tableName={draft.name}
          onTableNameChange={draftState.setTableName}
          originalName={structure.name}
          tableNameError={!!tableNameIssue("error")}
          tableNameWarning={tableNameWarning ? issueMessage(t, tableNameWarning) : undefined}
          comment={driverCanAlter(descriptor, "table_comment") ? draft.comment : null}
          onCommentChange={draftState.setTableComment}
          originalComment={structure.comment}
          counts={counts}
          indexCounts={indexCounts}
          isDirty={isDirty}
          errorCount={errors.length}
          loading={false}
          onRevertAll={() => confirmOrRun("revertAll")}
          onReload={() => confirmOrRun("reload")}
          canSave={canSave}
          onSave={() => save()}
          saveShortcut={formatBinding(saveBinding)}
        />
      }
      tabs={tabs}
      subTab={subTab}
      onSubTabChange={onSubTabChange}
      tabsRight={
        subTab === "fields" ? (
          <Button variant="ghost" size="xs" onClick={addField}>
            <Plus aria-hidden />
            {t("query.designer.addField")}
          </Button>
        ) : (
          subTab === "indexes" &&
          indexesEditable && (
            <Button variant="ghost" size="xs" onClick={addIndex}>
              <Plus aria-hidden />
              {t("query.designer.addIndex")}
            </Button>
          )
        )
      }
      footer={<IssueList issues={issues} onSelect={selectIssue} />}
      status={<DesignerStatus driver={descriptor?.name} fullName={`${database}.${table}`} />}
    >
      <TabsContent value="fields" className="flex min-h-0 flex-col">
        <CapabilityBar descriptor={descriptor} />
        <EditableFieldsGrid
          columns={draft.columns}
          descriptor={descriptor}
          showComment={showsColumnComments(descriptor, structure.columns)}
          issues={issues}
          selectedKey={selectedKey}
          primaryKeys={primaryKeys}
          onSelect={setSelectedKey}
          onChange={draftState.updateColumn}
        />
        {selected && (
          <ColumnProperties
            column={selected}
            descriptor={descriptor}
            onChange={draftState.updateColumn}
            onRevert={draftState.revertField}
          />
        )}
      </TabsContent>
      <TabsContent value="indexes" className="flex min-h-0 flex-row">
        {indexesEditable ? (
          <>
            <EditableIndexList
              draft={draft}
              descriptor={descriptor}
              showComment={showsIndexComments(descriptor, indexDefinitions)}
              issues={issues}
              selectedKey={selectedIndexKey}
              onSelect={setSelectedIndexKey}
              onDelete={draftState.deleteIndex}
              onRestore={draftState.restoreIndex}
            />
            {selectedIndex && (
              <IndexEditor
                draft={draft}
                index={selectedIndex}
                descriptor={descriptor}
                issues={issues.filter((i) => i.target.kind === "index" && i.target.key === selectedIndex.key)}
                onChange={draftState.updateIndex}
                onClose={() => setSelectedIndexKey(null)}
              />
            )}
          </>
        ) : (
          <IndexList
            indexes={structure.indexes}
            descriptor={descriptor}
            // 驱动没声明索引编辑(改表模型不带索引):索引只能查看,扩展驱动给出「去更新」。
            notEditableNotice
            updateDriverAction={<UpdateDriverAction descriptor={descriptor} />}
          />
        )}
      </TabsContent>
      <TabsContent value="foreignKeys" className="flex min-h-0 flex-col">
        <ForeignKeyList foreignKeys={structure.foreignKeys} />
      </TabsContent>
      <TabsContent value="sql" className="flex min-h-0 flex-col">
        <SqlPreviewPane
          preview={preview}
          droppedColumns={dropped}
          droppedIndexes={droppedIndexes}
          onOpenInSqlTab={(script) => openSqlTab(tabId, database, script)}
        />
      </TabsContent>
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={confirm === "reload" ? t("query.designer.reloadTitle") : t("query.designer.revertAllTitle")}
        description={confirm === "reload" ? t("query.designer.reloadDesc") : t("query.designer.revertAllDesc")}
        cancelText={t("action.cancel")}
        confirmText={confirm === "reload" ? t("query.designer.reloadConfirm") : t("query.designer.revertAllConfirm")}
        onConfirm={runConfirmed}
      />
      <CloseGuardDialog
        open={guarding}
        name={`${database}.${table}`}
        changes={changeCount}
        canSave={canSave}
        onStay={() => cancelCloseRequest(tabId)}
        onDiscard={() => closeRequest && forceCloseInnerTabs(tabId, closeRequest.ids, closeRequest.closeOuter)}
        onSave={() => {
          if (!closeRequest) return;
          cancelCloseRequest(tabId);
          save({ ids: closeRequest.ids, closeOuter: closeRequest.closeOuter });
        }}
      />
    </DesignerFrame>
  );
}
