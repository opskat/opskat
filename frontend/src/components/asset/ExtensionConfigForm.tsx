import { useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import {
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} from "@opskat/ui";
import { SecretInput } from "@/components/SecretInput";
import type { ExtensionConfigProperty, ExtensionConfigSchema } from "@/extension/configSchema";

interface ExtensionConfigFormProps {
  configSchema: ExtensionConfigSchema;
  value: Record<string, unknown>;
  onChange: (config: Record<string, unknown>) => void;
  /** 有已存值、但宿主没把明文交给表单的密码字段（按字段名取）：呈现为"已设置，留空则不修改"。 */
  withheldSecrets?: Record<string, string>;
  /** 保存校验返回的逐字段错误（按字段名）：显示在对应字段下方。 */
  fieldErrors?: Record<string, string>;
}

export function ExtensionConfigForm({
  configSchema,
  value,
  onChange,
  withheldSecrets,
  fieldErrors,
}: ExtensionConfigFormProps) {
  const { t } = useTranslation();
  const properties = configSchema.properties ?? {};
  const required = useMemo(() => new Set(configSchema.required ?? []), [configSchema.required]);
  const order = configSchema.propertyOrder;
  const fields = order
    ? order.filter((k) => k in properties).map((k) => [k, properties[k]] as const)
    : Object.entries(properties);

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
          <div key={key} className="grid gap-2">
            <Label>
              {label}
              {isRequired && <span className="text-destructive ml-0.5">*</span>}
            </Label>
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
            {description && <p className="text-xs text-muted-foreground">{description}</p>}
          </div>
        );
      }

      // Boolean → Switch
      if (prop.type === "boolean") {
        return (
          <div key={key} className="flex items-center justify-between">
            <div>
              <Label>{label}</Label>
              {description && <p className="text-xs text-muted-foreground">{description}</p>}
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
          <div key={key} className="grid gap-2">
            <Label htmlFor={key}>
              {label}
              {isRequired && <span className="text-destructive ml-0.5">*</span>}
            </Label>
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
            {description && <p className="text-xs text-muted-foreground">{description}</p>}
          </div>
        );
      }

      // Textarea (multi-line text; PEM, JSON blobs, etc.)
      if (prop.format === "textarea") {
        return (
          <div key={key} className="grid gap-2">
            <Label htmlFor={key}>
              {label}
              {isRequired && <span className="text-destructive ml-0.5">*</span>}
            </Label>
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
            {description && <p className="text-xs text-muted-foreground">{description}</p>}
          </div>
        );
      }

      // String (password or normal)
      return (
        <div key={key} className="grid gap-2">
          <Label htmlFor={key}>
            {label}
            {isRequired && <span className="text-destructive ml-0.5">*</span>}
          </Label>
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
          {description && <p className="text-xs text-muted-foreground">{description}</p>}
        </div>
      );
    },
    [value, required, updateField, withheldSecrets, t, errorProps, errorText]
  );

  return <>{fields.map(([key, prop]) => renderField(key, prop))}</>;
}
