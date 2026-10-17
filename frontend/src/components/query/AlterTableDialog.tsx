import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Plus, Trash2 } from "lucide-react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Switch,
} from "@opskat/ui";
import { ExecuteSQL, GetAlterTableModel, GetAlterTableStatements } from "../../../wailsjs/go/query/Query";
import { dbdriver } from "../../../wailsjs/go/models";
import { SqlPreviewDialog } from "./SqlPreviewDialog";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { driverCanAlter, findDatabaseDriver, useDatabaseDrivers } from "@/lib/dbDrivers";

interface AlterTableDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  database: string;
  /** 列表给出的表名(带 schema 分组的驱动是 schema.table)。 */
  table: string;
  /** 驱动 id;能改哪些属性按它的描述锁定。 */
  driver?: string;
  /** 执行成功后调用;改了名时带上改名后的表名(列表给出的形式)。 */
  onSuccess: (tableName?: string) => void;
}

/** 对话框里的一列:originalName 是它在表里现在的名字,新增的列没有。 */
interface DraftColumn {
  id: number;
  originalName?: string;
  name: string;
  type: string;
  nullable: boolean;
  defaultValue: string;
  comment: string;
  isNew: boolean;
}

/**
 * 驱动声明的可改属性恰好是下面某一组时,显示对应的限制说明与表注释输入框的占位文案。其余组合不显示说明,
 * 锁定的输入照样锁定。
 */
const ALTER_LIMITS: { attributes: string[]; hint: string; tableComment: string }[] = [
  {
    attributes: [],
    hint: "query.alterLimitsRenameAddDropOnly",
    tableComment: "query.alterTableCommentUnsupported",
  },
  {
    attributes: ["column_type", "column_nullable"],
    hint: "query.alterLimitsTypeAndNullabilityOnly",
    tableComment: "query.alterTableCommentExtendedProperties",
  },
];

function findAlterLimits(declared: string[]) {
  return ALTER_LIMITS.find(
    (limits) => limits.attributes.length === declared.length && limits.attributes.every((a) => declared.includes(a))
  );
}

function createNewDraft(id: number): DraftColumn {
  return {
    id,
    name: "",
    type: "VARCHAR(255)",
    nullable: true,
    defaultValue: "",
    comment: "",
    isNew: true,
  };
}

/** 对话框里的样子与打开时驱动读出的相比有没有改动(比较方式同驱动:类型不分大小写,默认值与注释去掉首尾空白)。 */
function draftChanged(
  model: dbdriver.AlterTableModel,
  tableNameDraft: string,
  tableCommentDraft: string,
  columns: DraftColumn[]
): boolean {
  const name = tableNameDraft.trim();
  if (name && name !== model.name) return true;
  if (tableCommentDraft.trim() !== model.comment.trim()) return true;
  const kept = new Set<string>();
  for (const col of columns) {
    if (col.isNew || !col.originalName) {
      if (col.name.trim()) return true;
      continue;
    }
    kept.add(col.originalName);
    const original = model.columns.find((c) => c.name === col.originalName);
    if (!original) continue;
    if (
      col.name.trim() !== original.name ||
      col.type.trim().toLowerCase() !== original.type.trim().toLowerCase() ||
      col.nullable !== original.nullable ||
      col.defaultValue.trim() !== original.defaultValue.trim() ||
      col.comment.trim() !== original.comment.trim()
    ) {
      return true;
    }
  }
  return model.columns.some((c) => !kept.has(c.name));
}

/** 交给驱动的改表请求:改完之后这张表的样子,以及要删除的已有列。 */
function buildRequest(
  model: dbdriver.AlterTableModel,
  tableNameDraft: string,
  tableCommentDraft: string,
  columns: DraftColumn[]
): dbdriver.AlterTableRequest {
  const kept = new Set(columns.filter((col) => !col.isNew && col.originalName).map((col) => col.originalName));
  return dbdriver.AlterTableRequest.createFrom({
    name: tableNameDraft.trim(),
    comment: tableCommentDraft,
    columns: columns.map((col) => ({
      originalName: col.isNew ? "" : (col.originalName ?? ""),
      name: col.name,
      type: col.type,
      nullable: col.nullable,
      defaultValue: col.defaultValue,
      comment: col.comment,
    })),
    drops: model.columns.filter((c) => !kept.has(c.name)).map((c) => c.name),
  });
}

export function AlterTableDialog({
  open,
  onOpenChange,
  assetId,
  database,
  table,
  driver,
  onSuccess,
}: AlterTableDialogProps) {
  const { t } = useTranslation();
  const [model, setModel] = useState<dbdriver.AlterTableModel | null>(null);
  const [columns, setColumns] = useState<DraftColumn[]>([]);
  const [tableNameDraft, setTableNameDraft] = useState("");
  const [tableCommentDraft, setTableCommentDraft] = useState("");
  const [nextId, setNextId] = useState(1);
  const [loadingColumns, setLoadingColumns] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [showSqlPreview, setShowSqlPreview] = useState(false);
  const [previewStatements, setPreviewStatements] = useState<string[]>([]);
  const [pendingNextTableName, setPendingNextTableName] = useState<string | undefined>();

  const driversState = useDatabaseDrivers();
  const descriptor =
    driversState.status === "ready" ? findDatabaseDriver(driversState.drivers, driver ?? "") : undefined;
  const can = (attribute: string) => driverCanAlter(descriptor, attribute);
  const limits = descriptor ? findAlterLimits(descriptor.alterTableAttributes ?? []) : undefined;
  const canTableComment = can("table_comment");

  const resetForm = useCallback(() => {
    setModel(null);
    setColumns([]);
    setTableNameDraft("");
    setTableCommentDraft("");
    setNextId(1);
    setShowSqlPreview(false);
    setPreviewStatements([]);
    setPendingNextTableName(undefined);
  }, []);

  // 表的列、类型、可空、默认值、注释与表注释都由驱动读出。
  const loadColumns = useCallback(async () => {
    if (!assetId || !open) return;

    try {
      const loaded = await GetAlterTableModel(assetId, database, table);
      setModel(loaded);
      setColumns(
        loaded.columns.map((col, idx) => ({
          id: idx + 1,
          originalName: col.name,
          name: col.name,
          type: col.type,
          nullable: col.nullable,
          defaultValue: col.defaultValue,
          comment: col.comment,
          isNew: false,
        }))
      );
      setTableNameDraft(loaded.name);
      setTableCommentDraft(loaded.comment);
      setNextId(loaded.columns.length + 1);
    } catch (e) {
      toast.error(String(e));
      setModel(null);
      setColumns([]);
      setTableCommentDraft("");
    } finally {
      setLoadingColumns(false);
    }
  }, [assetId, open, table, database]);

  // 打开(或资产/库/表变化)时同步进入加载态:渲染期对比上次值,替代 effect 里的同步 setState;
  // 异步拉取仍留在下面的 effect(loadColumns 内已无同步 setState)。
  const [prevLoadKey, setPrevLoadKey] = useState<{
    open: boolean;
    assetId?: number;
    table?: string;
    database?: string;
  }>({ open: false });
  if (
    open !== prevLoadKey.open ||
    assetId !== prevLoadKey.assetId ||
    table !== prevLoadKey.table ||
    database !== prevLoadKey.database
  ) {
    setPrevLoadKey({ open, assetId, table, database });
    if (open && assetId) setLoadingColumns(true);
  }

  useEffect(() => {
    if (open) {
      // loadColumns 的 setState 全部在 await 之后(异步续体),用 async 包装让规则识别。
      void (async () => {
        await loadColumns();
      })();
    }
  }, [open, loadColumns]);

  const hasPendingChanges = useMemo(
    () => !!model && draftChanged(model, tableNameDraft, tableCommentDraft, columns),
    [model, tableNameDraft, tableCommentDraft, columns]
  );

  const updateColumn = useCallback((id: number, patch: Partial<DraftColumn>) => {
    setColumns((prev) => prev.map((col) => (col.id === id ? { ...col, ...patch } : col)));
  }, []);

  const handleAddColumn = useCallback(() => {
    setColumns((prev) => [...prev, createNewDraft(nextId)]);
    setNextId((v) => v + 1);
  }, [nextId]);

  const handleRemoveColumn = useCallback((id: number) => {
    setColumns((prev) => prev.filter((col) => col.id !== id));
  }, []);

  const validateDraft = useCallback(() => {
    if (columns.length === 0) {
      toast.error(t("query.designTableAtLeastOneColumn"));
      return false;
    }

    const seen = new Set<string>();
    for (const col of columns) {
      const name = col.name.trim();
      if (!name) {
        toast.error(t("query.designTableColumnNameRequired"));
        return false;
      }
      if (!col.type.trim()) {
        toast.error(t("query.designTableColumnTypeRequired"));
        return false;
      }

      const lower = name.toLowerCase();
      if (seen.has(lower)) {
        toast.error(t("query.designTableDuplicateColumn", { name }));
        return false;
      }
      seen.add(lower);
    }

    return true;
  }, [columns, t]);

  const handlePreview = useCallback(async () => {
    if (!assetId || !model) return;
    if (!validateDraft()) return;

    // 语句由驱动对照表现在的定义生成;生成失败时显示错误、不打开预览、不执行任何语句。
    setGenerating(true);
    try {
      const result = await GetAlterTableStatements(
        assetId,
        database,
        table,
        buildRequest(model, tableNameDraft, tableCommentDraft, columns)
      );
      if (result.statements.length === 0) {
        toast.error(t("query.designTableNoChanges"));
        return;
      }
      setPreviewStatements(result.statements);
      setPendingNextTableName(result.table !== table ? result.table : undefined);
      setShowSqlPreview(true);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setGenerating(false);
    }
  }, [assetId, model, validateDraft, database, table, tableNameDraft, tableCommentDraft, columns, t]);

  const handleConfirmSubmit = useCallback(async () => {
    if (!assetId || previewStatements.length === 0) return;

    setSubmitting(true);
    try {
      for (const sql of previewStatements) {
        await ExecuteSQL(assetId, sql, database);
      }

      notifySuccess(t("query.alterTableSuccess"));
      setShowSqlPreview(false);
      onOpenChange(false);
      onSuccess(pendingNextTableName);
      resetForm();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSubmitting(false);
    }
  }, [assetId, previewStatements, database, t, onOpenChange, onSuccess, pendingNextTableName, resetForm]);

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(nextOpen) => {
          if (!submitting && !nextOpen) resetForm();
          onOpenChange(nextOpen);
        }}
      >
        <DialogContent className="w-[74vw] !max-w-none max-h-[92vh] overflow-hidden" showCloseButton={!submitting}>
          <DialogHeader>
            <DialogTitle>{t("query.designTableDialogTitle")}</DialogTitle>
            <DialogDescription>{t("query.designTableDialogDesc", { table })}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3 flex-1 min-h-0 overflow-hidden">
            <div className="space-y-1.5">
              <Label className="text-xs">{t("query.alterRenameTableLabel")}</Label>
              <Input
                className="h-8 text-xs font-mono"
                value={tableNameDraft}
                onChange={(e) => setTableNameDraft(e.target.value)}
                placeholder={t("query.alterRenameTablePlaceholder")}
                disabled={submitting || loadingColumns}
                data-testid="alter-table-name"
              />
            </div>

            <div className="space-y-1.5">
              <Label className="text-xs">{t("query.tableCommentLabel")}</Label>
              <Input
                className="h-8 text-xs font-mono"
                value={tableCommentDraft}
                onChange={(e) => setTableCommentDraft(e.target.value)}
                placeholder={
                  canTableComment
                    ? t("query.tableCommentPlaceholder")
                    : t(limits?.tableComment ?? "query.alterTableCommentUnsupported", { name: descriptor?.name })
                }
                disabled={submitting || loadingColumns || !canTableComment}
                data-testid="alter-table-comment"
              />
            </div>

            {limits && (
              <p className="text-[11px] text-muted-foreground">{t(limits.hint, { name: descriptor?.name })}</p>
            )}

            <div className="flex items-center justify-between">
              <Label className="text-xs">{t("query.designTableColumns")}</Label>
              <Button
                variant="outline"
                size="sm"
                className="h-7 text-xs gap-1"
                onClick={handleAddColumn}
                disabled={submitting || loadingColumns}
                data-testid="alter-add-column"
              >
                <Plus className="h-3.5 w-3.5" />
                {t("query.designTableAddColumn")}
              </Button>
            </div>

            {loadingColumns ? (
              <div className="flex items-center justify-center py-8 text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
              </div>
            ) : (
              <div className="max-h-[52vh] min-h-0 overflow-y-auto pr-2">
                <div className="space-y-2">
                  <div className="grid grid-cols-20 gap-2 px-1">
                    <Label className="col-span-3 text-[11px] text-muted-foreground">{t("query.columnNameLabel")}</Label>
                    <Label className="col-span-4 text-[11px] text-muted-foreground">{t("query.columnTypeLabel")}</Label>
                    <Label className="col-span-4 text-[11px] text-muted-foreground">
                      {t("query.defaultValueLabel")}
                    </Label>
                    <Label className="col-span-6 text-[11px] text-muted-foreground">
                      {t("query.columnCommentLabel")}
                    </Label>
                    <Label className="col-span-2 text-[11px] text-muted-foreground">
                      {t("query.columnNullableLabel")}
                    </Label>
                    <Label className="col-span-1 text-[11px] text-muted-foreground">&nbsp;</Label>
                  </div>

                  {columns.map((col) => {
                    // 新增的列总能给出类型、可空与默认值;已有列的这三项与所有列的注释按驱动的声明锁定。
                    const locked = (attribute: string) => !col.isNew && !can(attribute);
                    return (
                      <div key={col.id} className="rounded-md border border-border p-2">
                        <div className="grid grid-cols-20 gap-2 items-center">
                          <div className="col-span-3">
                            <Input
                              className="h-8 text-xs font-mono"
                              value={col.name}
                              onChange={(e) => updateColumn(col.id, { name: e.target.value })}
                              placeholder={t("query.columnNamePlaceholder")}
                              disabled={submitting}
                              data-testid="alter-column-name"
                            />
                          </div>
                          <div className="col-span-4">
                            <Input
                              className="h-8 text-xs font-mono"
                              value={col.type}
                              onChange={(e) => updateColumn(col.id, { type: e.target.value })}
                              placeholder={t("query.columnTypePlaceholder")}
                              disabled={submitting || locked("column_type")}
                              data-testid="alter-column-type"
                            />
                          </div>
                          <div className="col-span-4">
                            <Input
                              className="h-8 text-xs font-mono"
                              value={col.defaultValue}
                              onChange={(e) => updateColumn(col.id, { defaultValue: e.target.value })}
                              placeholder={t("query.defaultValuePlaceholder")}
                              disabled={submitting || locked("column_default")}
                              data-testid="alter-column-default"
                            />
                          </div>
                          <div className="col-span-6">
                            <Input
                              className="h-8 text-xs font-mono"
                              value={col.comment}
                              onChange={(e) => updateColumn(col.id, { comment: e.target.value })}
                              placeholder={t("query.columnCommentPlaceholder")}
                              disabled={submitting || !can("column_comment")}
                              data-testid="alter-column-comment"
                            />
                          </div>
                          <div className="col-span-2 flex items-center justify-center gap-1">
                            <Switch
                              checked={col.nullable}
                              onCheckedChange={(checked) => updateColumn(col.id, { nullable: checked })}
                              disabled={submitting || locked("column_nullable")}
                              data-testid="alter-column-nullable"
                            />
                            <span className="text-[11px] text-muted-foreground">
                              {col.nullable ? "NULL" : "NOT NULL"}
                            </span>
                          </div>
                          <div className="col-span-1 flex justify-end">
                            <Button
                              variant="ghost"
                              size="icon-xs"
                              className="h-8 w-8"
                              onClick={() => handleRemoveColumn(col.id)}
                              disabled={submitting || columns.length === 1}
                              title={t("query.removeColumn")}
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="h-8 text-xs"
              onClick={() => onOpenChange(false)}
              disabled={submitting}
            >
              {t("action.cancel")}
            </Button>
            <Button
              size="sm"
              className="h-8 text-xs"
              onClick={handlePreview}
              disabled={submitting || generating || loadingColumns || !hasPendingChanges}
              data-testid="alter-preview"
            >
              {submitting || generating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
              {t("query.designTablePreviewChanges")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <SqlPreviewDialog
        open={showSqlPreview}
        onOpenChange={(nextOpen) => {
          if (!submitting) setShowSqlPreview(nextOpen);
        }}
        statements={previewStatements}
        onConfirm={handleConfirmSubmit}
        submitting={submitting}
      />
    </>
  );
}
