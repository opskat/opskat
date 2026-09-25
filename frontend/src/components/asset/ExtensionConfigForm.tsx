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

interface JSONSchemaProperty {
  type?: string;
  format?: string;
  enum?: string[];
  title?: string;
  description?: string;
  placeholder?: string;
}

interface JSONSchema {
  type?: string;
  properties?: Record<string, JSONSchemaProperty>;
  required?: string[];
  propertyOrder?: string[];
}

interface ExtensionConfigFormProps {
  configSchema: JSONSchema;
  value: Record<string, unknown>;
  onChange: (config: Record<string, unknown>) => void;
  /** 有已存值、但宿主没把明文交给表单的密码字段（按字段名取）：呈现为"已设置，留空则不修改"。 */
  withheldSecrets?: Record<string, string>;
}

export function ExtensionConfigForm({ configSchema, value, onChange, withheldSecrets }: ExtensionConfigFormProps) {
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

  const renderField = useCallback(
    (key: string, prop: JSONSchemaProperty) => {
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
              <SelectTrigger>
                <SelectValue placeholder={placeholder} />
              </SelectTrigger>
              <SelectContent>
                {prop.enum.map((opt) => (
                  <SelectItem key={opt} value={opt}>
                    {opt}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
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
            />
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
            />
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
            />
          ) : (
            <Input
              id={key}
              value={String(value[key] ?? "")}
              onChange={(e) => updateField(key, e.target.value)}
              placeholder={placeholder}
            />
          )}
          {description && <p className="text-xs text-muted-foreground">{description}</p>}
        </div>
      );
    },
    [value, required, updateField, withheldSecrets, t]
  );

  return <>{fields.map(([key, prop]) => renderField(key, prop))}</>;
}
