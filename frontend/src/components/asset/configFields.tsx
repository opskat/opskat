import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch, Textarea } from "@opskat/ui";
import { Field, FieldLabel, Segmented } from "@/components/asset/fields";
import type { UseAssetCredential } from "@/components/asset/useAssetCredential";
import type { asset_entity } from "../../../wailsjs/go/models";

/** password/tunnel kind 渲染所需的横切依赖(Task 2b 使用)。 */
export interface FieldRenderCtx {
  cred?: UseAssetCredential;
  editAsset?: asset_entity.Asset;
}

type WithVisibility<S> = { visibleWhen?: (s: S) => boolean };

export type FieldDesc<S> = WithVisibility<S> &
  (
    | { kind: "text"; key: keyof S; label: string; placeholder?: string; required?: boolean; width?: string; testid?: string }
    | { kind: "number"; key: keyof S; label: string; placeholder?: string; min?: number; blankWhenZero?: boolean; width?: string; testid?: string }
    | { kind: "switch"; key: keyof S; label: string }
    | { kind: "select"; key: keyof S; label: string; options: { value: string; label: string }[]; testid?: string }
    | { kind: "segmented"; key: keyof S; label?: string; options: { value: string; label: string; testid?: string }[] }
    | { kind: "textarea"; key: keyof S; label: string; rows?: number; hint?: string; placeholder?: string }
    | { kind: "row"; fields: FieldDesc<S>[] }
    // ↓ composite kind 在 Task 2b 补实现;此处声明以锁定类型。
    | { kind: "password"; placeholder?: string; secretLabel?: string; selectSecretLabel?: string }
    | { kind: "tunnel"; tunnelOptionLabelKey?: string; tunnelSelectLabelKey?: string; excludeIds?: number[] }
    | { kind: "custom"; render: (s: S, patch: (p: Partial<S>) => void) => ReactNode }
  );

interface FieldsProps<S> {
  fields: FieldDesc<S>[];
  state: S;
  patch: (p: Partial<S>) => void;
  ctx?: FieldRenderCtx;
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
  if (field.visibleWhen && !field.visibleWhen(state)) return null;

  switch (field.kind) {
    case "text":
      return (
        <Field label={t(field.label)} required={field.required} className={field.width}>
          <Input
            data-testid={field.testid}
            value={String(state[field.key] ?? "")}
            placeholder={field.placeholder}
            onChange={(e) => patch({ [field.key]: e.target.value } as Partial<S>)}
          />
        </Field>
      );

    case "number": {
      const raw = state[field.key] as unknown as number;
      const display = field.blankWhenZero ? raw || "" : (raw ?? "");
      return (
        <Field label={t(field.label)} className={field.width}>
          <Input
            data-testid={field.testid}
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={field.min}
            value={display}
            placeholder={field.placeholder}
            onChange={(e) => {
              const n = Number(e.target.value);
              const next = field.min !== undefined ? Math.max(field.min, n || 0) : n;
              patch({ [field.key]: next } as Partial<S>);
            }}
          />
        </Field>
      );
    }

    case "switch":
      return (
        <div className="flex items-center justify-between">
          <FieldLabel>{t(field.label)}</FieldLabel>
          <Switch
            checked={!!state[field.key]}
            onCheckedChange={(v) => patch({ [field.key]: v } as Partial<S>)}
          />
        </div>
      );

    case "select":
      return (
        <Field label={t(field.label)}>
          <Select
            value={String(state[field.key] ?? "")}
            onValueChange={(v) => patch({ [field.key]: v } as Partial<S>)}
          >
            <SelectTrigger data-testid={field.testid} className="w-full">
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
        <Field label={field.label ? t(field.label) : undefined}>
          <Segmented
            value={String(state[field.key] ?? "")}
            onChange={(v) => patch({ [field.key]: v } as Partial<S>)}
            aria-label={field.label ? t(field.label) : undefined}
            options={field.options.map((o) => ({ value: o.value, label: t(o.label), testid: o.testid }))}
          />
        </Field>
      );

    case "textarea":
      return (
        <Field label={t(field.label)}>
          <Textarea
            value={String(state[field.key] ?? "")}
            rows={field.rows}
            placeholder={field.placeholder}
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

    // composite kind 在 Task 2b 实现;占位以保证 switch 穷尽。
    case "password":
    case "tunnel":
    case "custom":
      return null;
  }
}
