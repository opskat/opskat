import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Blocks, Download, Globe, Loader2, PencilLine, Plus, SquareTerminal, Trash2, Upload } from "lucide-react";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, ConfirmDialog } from "@opskat/ui";
import { notifySuccess } from "@/lib/notify";
import { getIconComponent } from "@/components/asset/IconPicker";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";
import { ImportCustomTypeDialog } from "@/components/settings/ImportCustomTypeDialog";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { ExportCustomType, SelectImportTypeFile } from "../../../wailsjs/go/customtype/CustomType";
import type { customtype } from "../../../wailsjs/go/models";

/** 设置 → 自定义类型:列表(执行方式 + 在用资产数)+ 新建 / 编辑 / 删除。 */
export function CustomTypeSection() {
  const { t } = useTranslation();
  const types = useCustomTypeStore((s) => s.types);
  const loading = useCustomTypeStore((s) => s.loading);
  const loaded = useCustomTypeStore((s) => s.loaded);
  const load = useCustomTypeStore((s) => s.load);
  const remove = useCustomTypeStore((s) => s.remove);
  const usage = useCustomTypeStore((s) => s.usage);

  const [editorOpen, setEditorOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | undefined>(undefined);
  const [deleteTarget, setDeleteTarget] = useState<customtype.Summary | null>(null);
  const [importPreview, setImportPreview] = useState<customtype.ImportPreview | null>(null);

  const [didLoad, setDidLoad] = useState(false);
  if (!didLoad && !loaded && !loading) {
    setDidLoad(true);
    void load();
  }

  const openCreate = () => {
    setEditingId(undefined);
    setEditorOpen(true);
  };
  const openEdit = (id: number) => {
    setEditingId(id);
    setEditorOpen(true);
  };

  const handleExport = async (id: number) => {
    try {
      await ExportCustomType(id);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleImport = async () => {
    try {
      const preview = await SelectImportTypeFile();
      if (preview) setImportPreview(preview);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const notifyInUse = (name: string, assets: string[]) =>
    toast.error(t("customType.deleteInUse", { name, count: assets.length, assets: assets.join(", ") }));

  // 先查占用:有资产在用就直接提示,不弹确认框;后端 Delete 的拦截仍兜底并发新增。
  const handleDeleteClick = async (ct: customtype.Summary) => {
    try {
      const assets = await usage(ct.id);
      if (assets.length > 0) {
        notifyInUse(ct.name, assets);
        return;
      }
      setDeleteTarget(ct);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleDeleteConfirm = async () => {
    if (!deleteTarget) return;
    try {
      const res = await remove(deleteTarget.id);
      if (!res.deleted) {
        notifyInUse(deleteTarget.name, res.assets ?? []);
      } else {
        notifySuccess(t("customType.deleted"));
      }
      setDeleteTarget(null);
    } catch (e) {
      toast.error(String(e));
    }
  };

  return (
    <>
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <div>
            <CardTitle className="text-base">{t("customType.sectionTitle")}</CardTitle>
            <CardDescription>{t("customType.sectionDesc")}</CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="gap-1"
              onClick={() => void handleImport()}
              data-testid="customtype-import-button"
            >
              <Upload className="h-3.5 w-3.5" />
              {t("customType.import")}
            </Button>
            <Button variant="outline" size="sm" className="gap-1" onClick={openCreate}>
              <Plus className="h-3.5 w-3.5" />
              {t("customType.new")}
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {loading && types.length === 0 ? (
            <div className="flex items-center justify-center py-8 text-muted-foreground">
              <Loader2 className="size-5 animate-spin text-primary" />
            </div>
          ) : types.length === 0 ? (
            <div className="py-8 text-center text-muted-foreground">
              <Blocks className="mx-auto mb-2 h-8 w-8 opacity-50" />
              <p className="text-sm">{t("customType.empty")}</p>
            </div>
          ) : (
            <div className="space-y-2">
              {types.map((ct) => {
                const Icon = getIconComponent(ct.icon);
                const ExecIcon = ct.execMode === "command" ? SquareTerminal : Globe;
                return (
                  <div
                    key={ct.id}
                    className="flex items-center justify-between gap-3 rounded-lg border border-border p-3"
                  >
                    <div className="flex min-w-0 items-center gap-3">
                      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded bg-muted">
                        <Icon className="h-4 w-4 text-muted-foreground" />
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <p className="truncate text-sm font-medium">{ct.name}</p>
                          <span className="truncate font-mono text-xs text-muted-foreground">{ct.slug}</span>
                        </div>
                        <p className="flex items-center gap-1 text-xs text-muted-foreground">
                          <ExecIcon className="h-3 w-3" />
                          <span>
                            {ct.execMode === "command" ? t("customType.execModeCommand") : t("customType.execModeHttp")}
                          </span>
                          <span className="mx-1">·</span>
                          <span>{t("customType.assetCount", { count: ct.assetCount })}</span>
                        </p>
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("customType.export")}
                        data-testid={`customtype-export-${ct.id}`}
                        onClick={() => void handleExport(ct.id)}
                      >
                        <Download className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("action.edit")}
                        onClick={() => openEdit(ct.id)}
                      >
                        <PencilLine className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("action.delete")}
                        className="text-muted-foreground hover:text-destructive"
                        onClick={() => void handleDeleteClick(ct)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      <CustomTypeEditorDialog
        open={editorOpen}
        typeId={editingId}
        onOpenChange={setEditorOpen}
        onSaved={() => void load()}
      />

      <ImportCustomTypeDialog
        preview={importPreview}
        onOpenChange={(open) => {
          if (!open) setImportPreview(null);
        }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t("customType.deleteConfirmTitle")}
        description={t("customType.deleteConfirmDesc", { name: deleteTarget?.name ?? "" })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={() => void handleDeleteConfirm()}
      />
    </>
  );
}
