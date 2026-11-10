import { useTranslation } from "react-i18next";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import type { ExtensionConfigSchema } from "@/extension/configSchema";

/** 驱动专属字段的表单块:沿用扩展配置表单渲染,保存被拒后把各字段的错误(i18n key)翻译后画在字段下。 */
export function DriverFieldsForm({
  schema,
  fieldErrors,
  ...form
}: {
  schema: ExtensionConfigSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  withheldSecrets: Record<string, unknown>;
  /** 字段 key → i18n key;保存被拒前为空。 */
  fieldErrors: Record<string, string>;
}) {
  const { t } = useTranslation();
  return (
    <ExtensionConfigForm
      configSchema={schema}
      fieldErrors={Object.fromEntries(Object.entries(fieldErrors).map(([k, key]) => [k, t(key)]))}
      {...form}
    />
  );
}
