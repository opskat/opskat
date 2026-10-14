import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { Loader2, RefreshCw, Save } from "lucide-react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import type { ReviewInfo } from "@/lib/commandReview";
import { ReviewNotice } from "@/components/approval/ReviewNotice";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { defaultValues, type ExtensionConfigSchema } from "@/extension/configSchema";
import {
  GetExtensionSettings,
  SaveExtensionSettings,
  TestCommandReviewer,
} from "../../../wailsjs/go/extension/Extension";
import type { extension_svc } from "../../../wailsjs/go/models";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

interface Loaded {
  schema: ExtensionConfigSchema;
  /** 已设置的密钥字段：后端不回值，表单按"已设置，留空则不修改"呈现。 */
  withheld: Record<string, string>;
}

/**
 * 扩展自己声明的设置（describe() 的 settingsSchema）。密钥字段只写不读：
 * 已设置的显示"已设置，留空则不修改"，留空保存时后端保留原来的密钥。
 * reviewer 表示这是命令审核扩展：多一个"保存并测试"，用刚保存的设置审一条无害命令。
 * 换一个扩展时由调用方用 key 重新挂载，不沿用上一个扩展的表单状态。
 */
export function ExtensionSettingsForm({ extensionName, reviewer }: { extensionName: string; reviewer?: boolean }) {
  const { t } = useTranslation();
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [value, setValue] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [testResult, setTestResult] = useState<ReviewInfo | null>(null);

  const apply = useCallback((view: extension_svc.ExtensionSettingsView) => {
    const schema = view.schema as ExtensionConfigSchema;
    setLoaded({ schema, withheld: Object.fromEntries(view.secretsSet.map((f) => [f, f])) });
    setValue({ ...defaultValues(schema), ...view.values });
  }, []);
  const load = useCallback(() => GetExtensionSettings(extensionName).then(apply), [extensionName, apply]);

  useEffect(() => {
    GetExtensionSettings(extensionName)
      .then(apply)
      .catch((e: unknown) => toast.error(errMsg(e)));
  }, [extensionName, apply]);

  // persist 保存表单；必填项没填时在字段下标出来并返回 false，不提交。
  const persist = async (): Promise<boolean> => {
    if (!loaded) return false;
    // 已存过的密钥留空表示不修改，算已填。
    const missing: Record<string, string> = {};
    for (const field of loaded.schema.required ?? []) {
      const v = value[field];
      if ((v === undefined || v === null || String(v) === "") && !loaded.withheld[field]) {
        missing[field] = t("extension.settings.required");
      }
    }
    setFieldErrors(missing);
    if (Object.keys(missing).length > 0) return false;
    // 设置统一按字符串保存（与 SaveExtensionSettings 的入参一致）；没填的字段不提交。
    const values: Record<string, string> = {};
    for (const [k, v] of Object.entries(value)) {
      if (v !== undefined && v !== null) values[k] = String(v);
    }
    await SaveExtensionSettings(extensionName, values);
    await load();
    return true;
  };

  const save = async () => {
    setBusy("save");
    try {
      if (await persist()) notifySuccess(t("extension.settings.saved"));
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setBusy(null);
    }
  };

  // 先保存再测：测的是刚保存的设置，不会拿还没保存的旧配置去测。
  const saveAndTest = async () => {
    setBusy("test");
    setTestResult(null);
    try {
      if (await persist()) setTestResult((await TestCommandReviewer(extensionName)) as ReviewInfo);
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setBusy(null);
    }
  };

  if (!loaded) {
    return (
      <div className="flex justify-center py-4">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  return (
    <div className="space-y-3" data-testid="extension-settings-form">
      <ExtensionConfigForm
        configSchema={loaded.schema}
        value={value}
        onChange={setValue}
        withheldSecrets={loaded.withheld}
        fieldErrors={fieldErrors}
      />
      <div className="flex flex-wrap gap-2">
        <Button onClick={save} disabled={busy !== null} variant="outline" className="gap-1">
          {busy === "save" ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
          {t("extension.settings.save")}
        </Button>
        {reviewer && (
          <Button onClick={saveAndTest} disabled={busy !== null} variant="outline" className="gap-1">
            {busy === "test" ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
            {t("extension.settings.saveAndTest")}
          </Button>
        )}
      </div>
      {testResult && (
        <div data-testid="extension-settings-test-result">
          <ReviewNotice review={testResult} />
        </div>
      )}
    </div>
  );
}
