import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Code2, Columns3, Link2, ListTree, Plus } from "lucide-react";
import { Button, ConfirmDialog, TabsContent } from "@opskat/ui";
import type { dbdriver } from "../../../../wailsjs/go/models";
import { useQueryStore } from "@/stores/queryStore";
import { driverCanAlter, type DriverDescriptor } from "@/lib/dbDrivers";
import { showsColumnComments, type TableStructure } from "./model";
import { droppedColumns, indexesDroppedWith, type DraftIssue } from "./draft";
import { useTableDraft } from "./useTableDraft";
import { useAlterPreview } from "./useAlterPreview";
import { issueMessage } from "./issueText";
import { DesignToolbar } from "./DesignerToolbar";
import { DesignerFrame, DesignerStatus, type DesignerSubTab, type SubTabDef } from "./DesignerFrame";
import { EditableFieldsGrid } from "./FieldsGrid";
import { ColumnProperties } from "./ColumnProperties";
import { CapabilityBar, IssueList } from "./DesignerNotices";
import { IndexList } from "./IndexList";
import { ForeignKeyList } from "./ForeignKeyList";
import { SqlPreviewPane } from "./SqlPreviewPane";

/** 保存时交给调用方的内容:驱动按当前草稿生成的语句(就是要执行的),请求本身,与改表后的表名。 */
export interface DesignerSave {
  statements: string[];
  request: dbdriver.AlterTableRequest;
  /** 改表后的表名(列表给出的形式);改了名时与打开时不同。 */
  table: string;
}

interface TableDesignerEditorProps {
  tabId: string;
  assetId: number;
  database: string;
  table: string;
  descriptor: DriverDescriptor | undefined;
  structure: TableStructure;
  subTab: DesignerSubTab;
  onSubTabChange: (tab: DesignerSubTab) => void;
  /** 重新读取表结构(草稿随之丢弃)。 */
  onReload: () => void;
  onSave?: (save: DesignerSave) => void;
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
  assetId,
  database,
  table,
  descriptor,
  structure,
  subTab,
  onSubTabChange,
  onReload,
  onSave,
}: TableDesignerEditorProps) {
  const { t } = useTranslation();
  const openSqlTab = useQueryStore((s) => s.openSqlTab);
  const draftState = useTableDraft(structure, descriptor);
  const { draft, counts, isDirty, issues, request } = draftState;
  const errors = issues.filter((i) => i.severity === "error");
  const preview = useAlterPreview({ assetId, database, table, request, enabled: errors.length === 0 });

  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<PendingConfirm>(null);
  const selected = draft.columns.find((c) => c.key === selectedKey && !c.deleted);

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
    } else if (confirm === "reload") {
      onReload();
    }
    setConfirm(null);
  };

  const statements = preview.status === "ready" ? preview.statements : [];
  const canSave = errors.length === 0 && preview.status === "ready" && statements.length > 0;
  const save = () => {
    if (preview.status !== "ready") return;
    onSave?.({ statements: preview.statements, request, table: preview.table });
  };

  const dropped = useMemo(() => droppedColumns(draft), [draft]);
  const droppedIndexes = useMemo(() => indexesDroppedWith(draft, structure.indexes), [draft, structure.indexes]);
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
    { id: "indexes", label: t("query.designer.tab.indexes"), icon: ListTree, count: structure.indexes.length },
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
          isDirty={isDirty}
          errorCount={errors.length}
          loading={false}
          onRevertAll={() => confirmOrRun("revertAll")}
          onReload={() => confirmOrRun("reload")}
          canSave={canSave}
          onSave={onSave ? save : undefined}
        />
      }
      tabs={tabs}
      subTab={subTab}
      onSubTabChange={onSubTabChange}
      tabsRight={
        subTab === "fields" && (
          <Button variant="ghost" size="xs" onClick={addField}>
            <Plus aria-hidden />
            {t("query.designer.addField")}
          </Button>
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
      <TabsContent value="indexes" className="flex min-h-0 flex-col">
        <IndexList
          indexes={structure.indexes}
          descriptor={descriptor}
          // 驱动没声明索引编辑(改表模型不带索引):索引只能查看。
          notEditableNotice={!structure.indexesEditable}
        />
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
    </DesignerFrame>
  );
}
