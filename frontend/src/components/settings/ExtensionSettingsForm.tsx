import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";
import { Button } from "@opskat/ui";
import i18n from "@/i18n";
import { notifySuccess } from "@/lib/notify";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { visibleFields, type ExtensionConfigSchema } from "@/extension/configSchema";
import type { ExtAuth } from "@/extension/types";
import { GetExtensionSettings, SaveExtensionSettings } from "../../../wailsjs/go/extension/Extension";

/** 宿主读回的设置表单：声明（已按扩展语言翻译）、非密钥的已存值、各密钥是否已设置。 */
interface SettingsFormData {
  configSchema: ExtensionConfigSchema;
  auth?: ExtAuth;
  values: Record<string, unknown>;
  secrets: Record<string, boolean>;
}

/**
 * 扩展设置表单（设置 › 扩展 › 详情）。字段渲染复用扩展资产类型的配置表单
 * （ExtensionConfigForm），校验由宿主按 configSchema 负责——表单侧不复制一份会漂移的规则：
 * 保存返回的逐字段错误显示在对应字段下，落不到字段上的另行提示，都不保存。
 * 密钥字段不回显：已设置的呈现为"已设置，留空则不修改"，留空保存即保留原值。
 * 扩展没有声明设置时什么也不渲染。
 */
export function ExtensionSettingsForm({ extName }: { extName: string }) {
  const { t } = useTranslation();
  const [form, setForm] = useState<SettingsFormData | null>(null);
  const [value, setValue] = useState<Record<string, unknown>>({});
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      const data = (await GetExtensionSettings(extName, i18n.language)) as SettingsFormData | null;
      setForm(data);
      setValue(data ? { ...data.values } : {});
    } catch (e) {
      toast.error(String((e as Error)?.message ?? e));
    }
  }, [extName]);

  useEffect(() => {
    // 加载体的 setState 都在 await 之后，经 async 边界调用避免 effect 内同步 setState
    void (async () => {
      await load();
    })();
  }, [load]);

  if (!form) return null;

  const handleSave = async () => {
    setSaving(true);
    try {
      const errors = (await SaveExtensionSettings(extName, JSON.stringify(value))) ?? [];
      if (errors.length > 0) {
        const shown = new Set(visibleFields(form.configSchema, form.auth, value).map(([key]) => key));
        const placed: Record<string, string> = {};
        for (const e of errors) {
          if (shown.has(e.field)) placed[e.field] = t(`extension.settings.error.${e.code}`);
        }
        setFieldErrors(placed);
        toast.error(t("extension.settings.invalid"));
        return;
      }
      setFieldErrors({});
      notifySuccess(t("extension.settings.saved"));
      await load();
    } catch (e) {
      toast.error(String((e as Error)?.message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div data-testid="extension-settings" className="space-y-3">
      <h4 className="text-sm font-medium">{t("extension.settings.title")}</h4>
      <div className="flex flex-col gap-3">
        <ExtensionConfigForm
          configSchema={form.configSchema}
          auth={form.auth}
          value={value}
          onChange={(next) => {
            // 改动过的字段清掉它的错误提示
            setFieldErrors((errs) =>
              Object.fromEntries(Object.entries(errs).filter(([key]) => next[key] === value[key]))
            );
            setValue(next);
          }}
          withheldSecrets={form.secrets}
          fieldErrors={fieldErrors}
        />
      </div>
      <div className="flex justify-end">
        <Button size="sm" onClick={handleSave} disabled={saving} data-testid="extension-settings-save">
          {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {t("action.save")}
        </Button>
      </div>
    </div>
  );
}
