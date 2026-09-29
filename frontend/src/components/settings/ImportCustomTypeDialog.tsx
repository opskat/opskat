import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { AlertTriangle, Globe, Loader2, SquareTerminal } from "lucide-react";
import { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Input, Label } from "@opskat/ui";
import { notifySuccess } from "@/lib/notify";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { custom_type_entity, customtype } from "../../../wailsjs/go/models";

interface ImportCustomTypeDialogProps {
  preview: customtype.ImportPreview | null;
  onOpenChange: (open: boolean) => void;
}

/** 设置 → 自定义类型 → 导入:预览(名称/标识/执行方式/字段/绑定)后确认创建。
 * 标识冲突时必须换一个新标识才能确认(Design decision 16,不能覆盖)。 */
export function ImportCustomTypeDialog({ preview, onOpenChange }: ImportCustomTypeDialogProps) {
  const { t } = useTranslation();
  const save = useCustomTypeStore((s) => s.save);

  const [name, setName] = useState(preview?.type?.name ?? "");
  const [slug, setSlug] = useState(preview?.type?.slug ?? "");
  const [saving, setSaving] = useState(false);
  const [issues, setIssues] = useState<custom_type_entity.Issue[]>([]);
  // 每次收到一个新的 preview(新文件 / 重新打开对话框)时,把标识/名称/校验提示重置
  // 为文件里的值 —— 渐进式渲染期间 setState,不是 effect(react-hooks/set-state-in-effect
  // 不允许在 effect body 里同步 setState;这是官方推荐的"根据 prop 变化调整 state"写法)。
  // 首次挂载时 name/slug 已经用上面的初始值取到 preview 的内容,这里只处理挂载
  // 之后 preview 本身换了一个新对象的情况。
  const [prevPreview, setPrevPreview] = useState(preview);
  if (preview !== prevPreview) {
    setPrevPreview(preview);
    setName(preview?.type?.name ?? "");
    setSlug(preview?.type?.slug ?? "");
    setIssues([]);
  }

  const open = !!preview;
  const rejected = !!preview && !preview.type;
  const ct = preview?.type;

  const handleConfirm = async () => {
    if (!ct) return;
    setSaving(true);
    try {
      const payload = new custom_type_entity.CustomType({
        ...ct,
        id: 0,
        slug: slug.trim(),
        name: name.trim(),
      });
      const res = await save(payload);
      if (res.issues && res.issues.length > 0) {
        setIssues(res.issues);
        return;
      }
      notifySuccess(t("customType.importSuccess"));
      onOpenChange(false);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg" data-testid="customtype-import-preview">
        <DialogHeader>
          <DialogTitle>{t("customType.importTitle")}</DialogTitle>
        </DialogHeader>

        {rejected && (
          <div className="space-y-2 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
            <div className="flex items-center gap-1.5 font-medium text-destructive">
              <AlertTriangle className="h-4 w-4" />
              {t("customType.importRejectedTitle")}
            </div>
            <ul className="list-inside list-disc text-muted-foreground">
              {(preview?.issues ?? []).map((is, i) => (
                <li key={i}>{is.message}</li>
              ))}
            </ul>
          </div>
        )}

        {ct && (
          <div className="space-y-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="grid gap-1.5">
                <Label>{t("customType.name")}</Label>
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="grid gap-1.5">
                <Label>{t("customType.slug")}</Label>
                <Input
                  data-testid="customtype-import-slug-input"
                  value={slug}
                  onChange={(e) => setSlug(e.target.value)}
                />
                {preview?.slugTaken && slug === ct.slug && (
                  <p className="text-xs text-destructive">{t("customType.importSlugTakenHint")}</p>
                )}
              </div>
            </div>

            <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
              {ct.execMode === "command" ? <SquareTerminal className="h-4 w-4" /> : <Globe className="h-4 w-4" />}
              {ct.execMode === "command" ? t("customType.execModeCommand") : t("customType.execModeHttp")}
            </div>

            <div className="grid gap-1.5">
              <Label className="text-xs text-muted-foreground">{t("customType.importFieldsLabel")}</Label>
              <div className="flex flex-wrap gap-1.5">
                {(ct.fields ?? []).map((f) => (
                  <span
                    key={f.name}
                    className="rounded border border-border px-2 py-0.5 font-mono text-xs text-muted-foreground"
                  >
                    {f.name}
                    {f.secret && <> · {t("customType.fieldSecret")}</>}
                    {f.required && <> · {t("customType.fieldRequired")}</>}
                  </span>
                ))}
              </div>
            </div>

            <div className="grid gap-1.5">
              <Label className="text-xs text-muted-foreground">{t("customType.importBindingsLabel")}</Label>
              {ct.execMode === "http" ? (
                <div className="space-y-1 text-sm">
                  <p className="font-mono text-xs text-muted-foreground">{ct.http?.base_url}</p>
                  {(ct.http?.auth ?? []).map((a, i) => (
                    <p key={i} className="text-xs text-muted-foreground">
                      {t(`customType.authType.${a.type}`)} {a.name ? `(${a.name})` : ""}
                    </p>
                  ))}
                </div>
              ) : (
                <div className="space-y-1 text-sm">
                  <p className="font-mono text-xs text-muted-foreground">
                    {ct.command?.template || t("customType.commandTemplatePlaceholder")}
                  </p>
                  {(ct.command?.env ?? []).map((e, i) => (
                    <p key={i} className="text-xs text-muted-foreground">
                      {e.name}
                    </p>
                  ))}
                </div>
              )}
            </div>

            {issues.length > 0 && (
              <div className="space-y-1 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
                {issues.map((is, i) => (
                  <p key={i} className="text-destructive">
                    {is.path}: {is.message}
                  </p>
                ))}
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          {ct && (
            <Button data-testid="customtype-import-confirm" onClick={() => void handleConfirm()} disabled={saving}>
              {saving && <Loader2 className="mr-1 h-4 w-4 animate-spin" />}
              {t("customType.importConfirm")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
