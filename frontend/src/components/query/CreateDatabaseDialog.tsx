import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
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
} from "@opskat/ui";
import { ExecuteSQL, GetCreateDatabaseStatements } from "../../../wailsjs/go/query/Query";
import { dbdriver } from "../../../wailsjs/go/models";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { SqlPreviewDialog } from "./SqlPreviewDialog";

interface CreateDatabaseDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  defaultDatabase: string;
  /** 驱动声明的建库选项字段与默认值(Descriptor.createDatabaseOptions);对话框只显示这些。 */
  options: dbdriver.DatabaseOption[];
  onSuccess: () => void;
}

/** 驱动可声明的建库选项(Go 侧 dbdriver.Option*)对应的标签与占位文案。 */
const OPTION_TEXT: Record<string, { label: string; placeholder: string }> = {
  charset: { label: "query.charsetLabel", placeholder: "query.charsetPlaceholder" },
  collation: { label: "query.collationLabel", placeholder: "query.collationPlaceholder" },
};

function isSafeOption(value: string): boolean {
  return /^[A-Za-z0-9_.-]+$/.test(value);
}

export function CreateDatabaseDialog({
  open,
  onOpenChange,
  assetId,
  defaultDatabase,
  options,
  onSuccess,
}: CreateDatabaseDialogProps) {
  const { t } = useTranslation();
  const [databaseName, setDatabaseName] = useState("");
  // 用户改过的选项值;没改过的字段显示驱动声明的默认值(驱动描述晚到时也跟着变)。
  const [edited, setEdited] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [showSqlPreview, setShowSqlPreview] = useState(false);
  const [previewStatements, setPreviewStatements] = useState<string[]>([]);
  const [pendingName, setPendingName] = useState("");

  const valueOf = useCallback((option: dbdriver.DatabaseOption) => edited[option.key] ?? option.default, [edited]);

  const resetForm = useCallback(() => {
    setDatabaseName("");
    setEdited({});
  }, []);

  const handlePreview = useCallback(async () => {
    if (!assetId) return;

    const name = databaseName.trim();
    if (!name) {
      toast.error(t("query.createDatabaseNameRequired"));
      return;
    }
    const values = Object.fromEntries(options.map((option) => [option.key, valueOf(option).trim()]));
    if (Object.values(values).some((value) => value && !isSafeOption(value))) {
      toast.error(t("query.createDatabaseOptionInvalid"));
      return;
    }

    // 语句由驱动生成;生成失败时显示错误、不打开预览、不执行任何语句。
    try {
      const statements = await GetCreateDatabaseStatements(
        assetId,
        defaultDatabase,
        dbdriver.CreateDatabaseRequest.createFrom({ name, options: values })
      );
      setPreviewStatements(statements);
      setPendingName(name);
      setShowSqlPreview(true);
    } catch (e) {
      toast.error(String(e));
    }
  }, [assetId, databaseName, options, valueOf, defaultDatabase, t]);

  const handleConfirmSubmit = useCallback(async () => {
    if (!assetId || previewStatements.length === 0) return;

    setSubmitting(true);
    try {
      for (const sql of previewStatements) {
        await ExecuteSQL(assetId, sql, defaultDatabase);
      }
      notifySuccess(t("query.createDatabaseSuccess", { database: pendingName }));
      setShowSqlPreview(false);
      onOpenChange(false);
      onSuccess();
      resetForm();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSubmitting(false);
    }
  }, [assetId, previewStatements, defaultDatabase, t, pendingName, onOpenChange, onSuccess, resetForm]);

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(nextOpen) => {
          if (!submitting && !nextOpen) resetForm();
          onOpenChange(nextOpen);
        }}
      >
        <DialogContent className="max-w-lg" showCloseButton={!submitting}>
          <DialogHeader>
            <DialogTitle>{t("query.createDatabaseDialogTitle")}</DialogTitle>
            <DialogDescription>{t("query.createDatabaseDialogDesc")}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label className="text-xs">{t("query.databaseNameLabel")}</Label>
              <Input
                className="h-8 text-xs font-mono"
                value={databaseName}
                onChange={(e) => setDatabaseName(e.target.value)}
                placeholder={t("query.databaseNamePlaceholder")}
                disabled={submitting}
              />
            </div>
            {options.map((option) => {
              const text = OPTION_TEXT[option.key];
              return (
                <div key={option.key} className="space-y-1.5">
                  <Label className="text-xs">{t(text.label)}</Label>
                  <Input
                    className="h-8 text-xs font-mono"
                    value={valueOf(option)}
                    onChange={(e) => setEdited((prev) => ({ ...prev, [option.key]: e.target.value }))}
                    placeholder={t(text.placeholder)}
                    disabled={submitting}
                  />
                </div>
              );
            })}
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
            <Button size="sm" className="h-8 text-xs" onClick={handlePreview} disabled={submitting}>
              {submitting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
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
