import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch, Textarea } from "@opskat/ui";
import { Field, FieldLabel, Segmented } from "@/components/asset/fields";
import type { UseAssetCredential } from "@/components/asset/useAssetCredential";
import type { asset_entity } from "../../../wailsjs/go/models";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { proxyChainLayerErrors, type ConnectionFormFields } from "@/components/asset/proxyConfig";
import type { ConfigGroup } from "@/components/asset/ConfigTabs";
import { useShownError } from "@/components/asset/useConfigSection";

/** password/tunnel 字段渲染所需的共享上下文。 */
export interface FieldRenderCtx {
  cred?: UseAssetCredential;
  editAsset?: asset_entity.Asset;
}

type WithRules<S> = {
  visibleWhen?: (s: S) => boolean;
  /**
   * 该字段当前的校验错误(i18n key,空串 = 无错)。与 required 一样只在保存被拒后显示,并让所在
   * 页签带标记;区块的 validate 应基于同一条规则,两边才不会说法不一。custom 字段经 render 的
   * 第三个参数拿到要显示的错误文案,自己决定画在哪个控件下。
   */
  error?: (s: S) => string;
};

export type FieldDesc<S> = WithRules<S> &
  (
    | {
        kind: "text";
        key: keyof S;
        label: string;
        placeholder?: string;
        required?: boolean;
        width?: string;
        testid?: string;
      }
    | {
        kind: "number";
        key: keyof S;
        label: string;
        placeholder?: string;
        min?: number;
        max?: number;
        blankWhenZero?: boolean;
        width?: string;
        testid?: string;
      }
    | { kind: "switch"; key: keyof S; label: string; description?: string }
    | { kind: "select"; key: keyof S; label: string; options: { value: string; label: string }[]; testid?: string }
    | {
        kind: "segmented";
        key: keyof S;
        label?: string;
        ariaLabel?: string;
        width?: string;
        options: { value: string; label: string; testid?: string }[];
      }
    | {
        kind: "textarea";
        key: keyof S;
        label: string;
        rows?: number;
        hint?: string;
        placeholder?: string;
        required?: boolean;
        mono?: boolean;
        testid?: string;
      }
    | { kind: "row"; fields: FieldDesc<S>[] }
    | {
        kind: "password";
        placeholder?: string;
        secretLabel?: string;
        selectSecretLabel?: string;
        usernameKey?: keyof S;
      }
    | { kind: "tunnel"; excludeIds?: number[] }
    | { kind: "custom"; render: (s: S, patch: (p: Partial<S>) => void, error?: string) => ReactNode }
  );

interface FieldsProps<S> {
  fields: FieldDesc<S>[];
  state: S;
  patch: (p: Partial<S>) => void;
  ctx?: FieldRenderCtx;
}

/** 字段自身的错误 key:声明的 error 规则优先,其次是必填的文本/多行字段留空;不可见的字段没有错误。 */
function fieldErrorKey<S>(field: FieldDesc<S>, state: S): string {
  if (field.visibleWhen && !field.visibleWhen(state)) return "";
  const declared = field.error?.(state);
  if (declared) return declared;
  if ((field.kind === "text" || field.kind === "textarea") && field.required) {
    return String(state[field.key] ?? "").trim() === "" ? "asset.generic.required" : "";
  }
  return "";
}

/** 一组字段里有没有会在保存被拒时画在字段下的错误(不可见的字段不算)。 */
function fieldsHaveError<S>(fields: FieldDesc<S>[], state: S): boolean {
  return fields.some((f) => {
    if (f.visibleWhen && !f.visibleWhen(state)) return false;
    if (fieldErrorKey(f, state)) return true;
    if (f.kind === "row") return fieldsHaveError(f.fields, state);
    if (f.kind === "tunnel") {
      // 与 ConnectionMethodFields 同一判定:只有代理链方式才显示(也才校验)各层的错误。
      const conn = state as unknown as ConnectionFormFields;
      return conn.connectionType !== "direct" && proxyChainLayerErrors(conn.proxyChainLayers ?? []).length > 0;
    }
    return false;
  });
}

/** 把字段描述符数组渲染成竖直列(复刻各 section 的 `flex flex-col gap-4`)。 */
export function Fields<S>({ fields, state, patch, ctx }: FieldsProps<S>) {
  return (
    <div className="flex flex-col gap-4">
      {fields.map((f, i) => (
        <FieldNode key={i} field={f} state={state} patch={patch} ctx={ctx} />
      ))}
    </div>
  );
}

function FieldNode<S>({
  field,
  state,
  patch,
  ctx,
}: {
  field: FieldDesc<S>;
  state: S;
  patch: (p: Partial<S>) => void;
  ctx?: FieldRenderCtx;
}) {
  const { t } = useTranslation();
  const errorKey = fieldErrorKey(field, state);
  const invalid = useShownError(errorKey !== "");
  if (field.visibleWhen && !field.visibleWhen(state)) return null;
  const error = invalid ? t(errorKey) : undefined;

  switch (field.kind) {
    case "text":
      return (
        <Field label={t(field.label)} required={field.required} error={error} className={field.width}>
          <Input
            data-testid={field.testid}
            aria-invalid={invalid || undefined}
            value={String(state[field.key] ?? "")}
            placeholder={field.placeholder ? t(field.placeholder, { defaultValue: field.placeholder }) : undefined}
            onChange={(e) => patch({ [field.key]: e.target.value } as Partial<S>)}
          />
        </Field>
      );

    case "number": {
      const raw = state[field.key] as unknown as number;
      const display = field.blankWhenZero ? raw || "" : (raw ?? "");
      return (
        <Field label={t(field.label)} error={error} className={field.width}>
          <Input
            data-testid={field.testid}
            aria-invalid={invalid || undefined}
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={field.min}
            max={field.max}
            value={display}
            placeholder={field.placeholder ? t(field.placeholder, { defaultValue: field.placeholder }) : undefined}
            onChange={(e) => {
              const n = Number(e.target.value);
              let next = field.min !== undefined ? Math.max(field.min, n || 0) : n;
              if (field.max !== undefined) next = Math.min(field.max, next);
              patch({ [field.key]: next } as Partial<S>);
            }}
          />
        </Field>
      );
    }

    case "switch":
      return (
        <div className="flex items-center justify-between gap-4">
          <div className="flex min-w-0 flex-col gap-0.5">
            <FieldLabel>{t(field.label)}</FieldLabel>
            {field.description && (
              <span className="text-[11px] leading-snug text-muted-foreground/70">{t(field.description)}</span>
            )}
          </div>
          <Switch
            aria-label={t(field.label)}
            checked={!!state[field.key]}
            onCheckedChange={(v) => patch({ [field.key]: v } as Partial<S>)}
          />
        </div>
      );

    case "select":
      return (
        <Field label={t(field.label)} error={error}>
          <Select value={String(state[field.key] ?? "")} onValueChange={(v) => patch({ [field.key]: v } as Partial<S>)}>
            <SelectTrigger data-testid={field.testid} aria-invalid={invalid || undefined} className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {field.options.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {t(o.label)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      );

    case "segmented":
      return (
        <Field label={field.label ? t(field.label) : undefined} className={field.width}>
          <Segmented
            value={String(state[field.key] ?? "")}
            onChange={(v) => patch({ [field.key]: v } as Partial<S>)}
            aria-label={field.ariaLabel ? t(field.ariaLabel) : field.label ? t(field.label) : undefined}
            options={field.options.map((o) => ({ value: o.value, label: t(o.label), testid: o.testid }))}
          />
        </Field>
      );

    case "textarea":
      return (
        <Field label={t(field.label)} required={field.required} error={error}>
          <Textarea
            data-testid={field.testid}
            aria-invalid={invalid || undefined}
            value={String(state[field.key] ?? "")}
            rows={field.rows}
            placeholder={field.placeholder ? t(field.placeholder, { defaultValue: field.placeholder }) : undefined}
            className={field.mono ? "font-mono text-sm" : undefined}
            onChange={(e) => patch({ [field.key]: e.target.value } as Partial<S>)}
          />
          {field.hint && <p className="text-xs text-muted-foreground">{t(field.hint)}</p>}
        </Field>
      );

    case "row":
      return (
        <div className="flex items-end gap-3">
          {field.fields.map((f, i) => (
            <FieldNode key={i} field={f} state={state} patch={patch} ctx={ctx} />
          ))}
        </div>
      );

    case "password": {
      const cred = ctx?.cred;
      if (!cred) return null; // password 字段要求调用方提供 ctx.cred
      return (
        <PasswordSourceField
          source={cred.value.passwordSource}
          onSourceChange={cred.setPasswordSource}
          password={cred.value.password}
          onPasswordChange={cred.setPassword}
          credentialId={cred.value.passwordCredentialId}
          onCredentialIdChange={cred.setPasswordCredentialId}
          managedPasswords={cred.managedPasswords}
          hasExistingPassword={!!cred.value.encryptedPassword}
          editAssetId={ctx?.editAsset?.ID}
          onUsernameChange={(v) => patch({ [field.usernameKey ?? "username"]: v } as unknown as Partial<S>)}
          placeholder={field.placeholder ? t(field.placeholder, { defaultValue: field.placeholder }) : undefined}
          secretLabel={field.secretLabel ? t(field.secretLabel, { defaultValue: field.secretLabel }) : undefined}
          selectSecretLabel={
            field.selectSecretLabel ? t(field.selectSecretLabel, { defaultValue: field.selectSecretLabel }) : undefined
          }
        />
      );
    }

    case "tunnel":
      return (
        <ConnectionMethodFields
          value={state as unknown as ConnectionFormFields}
          onChange={patch as unknown as (p: Partial<ConnectionFormFields>) => void}
          excludeIds={field.excludeIds}
        />
      );

    case "custom":
      return <>{field.render(state, patch, error)}</>;
  }
}

/** error:逃逸口组(render)自己画字段错误时,用它让页签带标记、保存被拒时切过来;声明式组由字段规则推出。 */
export type ConfigGroupSchema<S> =
  | { key: string; label: string; badge?: number; fields: FieldDesc<S>[] }
  | { key: string; label: string; badge?: number; error?: boolean; render: () => ReactNode };

/** 把组级 schema 转成 <ConfigTabs> 吃的 ConfigGroup[]:声明式组包成 <Fields>,逃逸口组透传 render。
 *  纯函数(不调 hook);render 闭包在 ConfigTabs 渲染期被调用。 */
// eslint-disable-next-line react-refresh/only-export-components -- group-assembler intentionally co-located with its <Fields> renderer
export function buildConfigGroups<S>(
  schema: ConfigGroupSchema<S>[],
  args: { state: S; patch: (p: Partial<S>) => void; ctx?: FieldRenderCtx }
): ConfigGroup[] {
  return schema.map((g) => ({
    key: g.key,
    label: g.label,
    badge: g.badge,
    error: "fields" in g ? fieldsHaveError(g.fields, args.state) : !!g.error,
    render:
      "render" in g
        ? g.render
        : () => <Fields fields={g.fields} state={args.state} patch={args.patch} ctx={args.ctx} />,
  }));
}

/** 端口取值规则(1–65535),供各区块的 validate 与端口字段的 error 规则共用。 */
// eslint-disable-next-line react-refresh/only-export-components -- shared field rule co-located with the descriptors that use it
export function portRangeError(port: number): string {
  return Number.isInteger(port) && port > 0 && port <= 65535 ? "" : "network.proxy.error.portRange";
}
