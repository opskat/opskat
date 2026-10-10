import { useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch, Textarea } from "@opskat/ui";
import { SecretInput } from "@/components/SecretInput";
import { FieldLabel } from "@/components/asset/fields";
import { visibleFields, type ExtensionConfigProperty, type ExtensionConfigSchema } from "@/extension/configSchema";
import type { ExtAuth } from "@/extension/types";

interface ExtensionConfigFormProps {
  configSchema: ExtensionConfigSchema;
  /** 类型声明的认证方式：只呈现当前选中方式用到的字段。 */
  auth?: ExtAuth;
  value: Record<string, unknown>;
  onChange: (config: Record<string, unknown>) => void;
  /** 有已存值、但宿主没把明文交给表单的密码字段（按字段名取）：呈现为"已设置，留空则不修改"。 */
  withheldSecrets?: Record<string, string>;
  /** 保存校验返回的逐字段错误（按字段名）：显示在对应字段下方。 */
  fieldErrors?: Record<string, string>;
}

export function ExtensionConfigForm({
  configSchema,
  auth,
  value,
  onChange,
  withheldSecrets,
  fieldErrors,
}: ExtensionConfigFormProps) {
  const { t } = useTranslation();
  const required = useMemo(() => new Set(configSchema.required ?? []), [configSchema.required]);
  const fields = visibleFields(configSchema, auth, value);

  const updateField = useCallback(
    (key: string, fieldValue: unknown) => {
      onChange({ ...value, [key]: fieldValue });
    },
    [value, onChange]
  );

  // 字段的错误提示与 aria 标记；无错误时两者都为空。
  const errorProps = useCallback(
    (key: string) => ({
      "aria-invalid": fieldErrors?.[key] ? (true as const) : undefined,
    }),
    [fieldErrors]
  );
  const errorText = useCallback(
    (key: string) => (fieldErrors?.[key] ? <p className="text-xs text-destructive">{fieldErrors[key]}</p> : null),
    [fieldErrors]
  );

  const renderField = useCallback(
    (key: string, prop: ExtensionConfigProperty) => {
      // Config schema values are already translated by the backend
      const label = prop.title || key;
      const description = prop.description || "";
      const placeholder = prop.placeholder || "";
      const isRequired = required.has(key);

      // Enum → Select
      if (prop.enum && prop.enum.length > 0) {
        return (
          <div key={key} className="flex flex-col gap-[7px]">
            <FieldLabel required={isRequired}>{label}</FieldLabel>
            <Select value={String(value[key] ?? "")} onValueChange={(v) => updateField(key, v)}>
              <SelectTrigger className="w-full" {...errorProps(key)}>
                <SelectValue placeholder={placeholder} />
              </SelectTrigger>
              <SelectContent>
                {prop.enum.map((opt, i) => (
                  <SelectItem key={opt} value={opt}>
                    {prop.enumLabels?.[i] ?? opt}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {errorText(key)}
            {description && <p className="text-[11px] leading-snug text-muted-foreground/70">{description}</p>}
          </div>
        );
      }

      // Boolean → Switch
      if (prop.type === "boolean") {
        return (
          <div key={key} className="flex items-center justify-between gap-4">
            <div className="flex min-w-0 flex-col gap-0.5">
              <FieldLabel>{label}</FieldLabel>
              {description && <span className="text-[11px] leading-snug text-muted-foreground/70">{description}</span>}
            </div>
            <Switch checked={!!value[key]} onCheckedChange={(v) => updateField(key, v)} />
          </div>
        );
      }

      // Numeric → number input that stores a number. The configSchema is the same
      // declaration the guest unmarshals into its Go struct, so storing "5" for an
      // `integer` property saves an asset every later tool call rejects with
      // `cannot unmarshal string into Go struct field ... of type int`.
      if (prop.type === "integer" || prop.type === "number") {
        return (
          <div key={key} className="flex flex-col gap-[7px]">
            <FieldLabel htmlFor={key} required={isRequired}>
              {label}
            </FieldLabel>
            <Input
              id={key}
              type="number"
              value={value[key] === undefined || value[key] === null ? "" : String(value[key])}
              // Empty means "not set", not 0: the property is dropped from the config
              // rather than saved as a value the user never chose.
              onChange={(e) => updateField(key, e.target.value === "" ? undefined : Number(e.target.value))}
              placeholder={placeholder}
              {...errorProps(key)}
            />
            {errorText(key)}
            {description && <p className="text-[11px] leading-snug text-muted-foreground/70">{description}</p>}
          </div>
        );
      }

      // Textarea (multi-line text; PEM, JSON blobs, etc.)
      if (prop.format === "textarea") {
        return (
          <div key={key} className="flex flex-col gap-[7px]">
            <FieldLabel htmlFor={key} required={isRequired}>
              {label}
            </FieldLabel>
            <Textarea
              id={key}
              value={String(value[key] ?? "")}
              onChange={(e) => updateField(key, e.target.value)}
              placeholder={placeholder}
              rows={6}
              className="font-mono text-xs"
              {...errorProps(key)}
            />
            {errorText(key)}
            {description && <p className="text-[11px] leading-snug text-muted-foreground/70">{description}</p>}
          </div>
        );
      }

      // String (password or normal)
      return (
        <div key={key} className="flex flex-col gap-[7px]">
          <FieldLabel htmlFor={key} required={isRequired}>
            {label}
          </FieldLabel>
          {prop.format === "password" ? (
            <SecretInput
              id={key}
              value={String(value[key] ?? "")}
              onChange={(e) => updateField(key, e.target.value)}
              placeholder={withheldSecrets?.[key] ? t("asset.passwordUnchanged") : placeholder || "••••••••"}
              {...errorProps(key)}
            />
          ) : (
            <Input
              id={key}
              value={String(value[key] ?? "")}
              onChange={(e) => updateField(key, e.target.value)}
              placeholder={placeholder}
              {...errorProps(key)}
            />
          )}
          {errorText(key)}
          {description && <p className="text-[11px] leading-snug text-muted-foreground/70">{description}</p>}
        </div>
      );
    },
    [value, required, updateField, withheldSecrets, t, errorProps, errorText]
  );

  return <>{fields.map(([key, prop]) => renderField(key, prop))}</>;
}
