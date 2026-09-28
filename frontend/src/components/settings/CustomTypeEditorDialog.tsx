import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Globe, SquareTerminal, Plus, Trash2, Lock, LockOpen, Loader2, Info } from "lucide-react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
  cn,
} from "@opskat/ui";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { IconPicker } from "@/components/asset/IconPicker";
import { PolicyTagEditor } from "@/components/asset/PolicyTagEditor";
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";
import { Field, Segmented } from "@/components/asset/fields";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { custom_type_entity } from "../../../wailsjs/go/models";

// 认证类型的值个数 / 是否带名称,与 internal/pkg/authtmpl.builtinAuthTypes 一一对应
// (header/query 各一个值 + 名称,basic 两个值 + 无名称)。
const AUTH_VALUE_COUNT: Record<string, number> = { header: 1, query: 1, basic: 2 };
const AUTH_HAS_NAME: Record<string, boolean> = { header: true, query: true, basic: false };
const AUTH_KINDS = ["header", "query", "basic"] as const;

interface FieldDraft {
  name: string;
  label: string;
  placeholder: string;
  secret: boolean;
  required: boolean;
  default: string;
}

interface AuthDraft {
  type: string;
  name: string;
  values: string[];
}

interface EnvDraft {
  name: string;
  value: string;
}

interface CustomTypeDraft {
  id: number;
  slug: string;
  name: string;
  icon: string;
  execMode: string;
  fields: FieldDraft[];
  httpBaseUrl: string;
  httpAuth: AuthDraft[];
  commandTemplate: string;
  commandEnv: EnvDraft[];
  usage: string;
  allowList: string[];
  denyList: string[];
}

function emptyField(): FieldDraft {
  return { name: "", label: "", placeholder: "", secret: false, required: false, default: "" };
}

function emptyDraft(): CustomTypeDraft {
  return {
    id: 0,
    slug: "",
    name: "",
    icon: "",
    // custom_type_entity 只导出实体类,执行方式常量("http"/"command")在后端,
    // 这里直接写字面量(与 custom_type_entity.ExecModeHTTP 的值保持一致)。
    execMode: "http",
    fields: [emptyField()],
    httpBaseUrl: "",
    httpAuth: [],
    commandTemplate: "",
    commandEnv: [],
    usage: "",
    allowList: [],
    denyList: [],
  };
}

function fromWire(ct: custom_type_entity.CustomType): CustomTypeDraft {
  return {
    id: ct.id,
    slug: ct.slug,
    name: ct.name,
    icon: ct.icon,
    execMode: ct.execMode,
    fields: (ct.fields ?? []).map((f) => ({
      name: f.name,
      label: f.label,
      placeholder: f.placeholder ?? "",
      secret: f.secret,
      required: f.required,
      default: f.default ?? "",
    })),
    httpBaseUrl: ct.http?.base_url ?? "",
    httpAuth: (ct.http?.auth ?? []).map((a) => ({ type: a.type, name: a.name ?? "", values: a.values ?? [] })),
    commandTemplate: ct.command?.template ?? "",
    commandEnv: (ct.command?.env ?? []).map((e) => ({ name: e.name, value: e.value })),
    usage: ct.usage ?? "",
    allowList: ct.defaultPolicy?.allow_list ?? [],
    denyList: ct.defaultPolicy?.deny_list ?? [],
  };
}

function toWire(d: CustomTypeDraft): custom_type_entity.CustomType {
  return new custom_type_entity.CustomType({
    id: d.id,
    slug: d.slug.trim(),
    name: d.name.trim(),
    icon: d.icon,
    execMode: d.execMode,
    fields: d.fields.map((f) => ({
      name: f.name.trim(),
      label: f.label.trim(),
      placeholder: f.placeholder.trim() || undefined,
      secret: f.secret,
      required: f.required,
      default: f.secret ? undefined : f.default || undefined,
    })),
    http:
      d.execMode === "http"
        ? {
            base_url: d.httpBaseUrl.trim(),
            auth: d.httpAuth.map((a) => ({
              type: a.type,
              name: AUTH_HAS_NAME[a.type] ? a.name.trim() : undefined,
              values: a.values,
            })),
          }
        : undefined,
    command:
      d.execMode === "command"
        ? {
            template: d.commandTemplate,
            env: d.commandEnv.map((e) => ({ name: e.name.trim(), value: e.value })),
          }
        : undefined,
    usage: d.usage,
    defaultPolicy: { allow_list: d.allowList, deny_list: d.denyList },
  });
}

function buildIssueMap(issues: custom_type_entity.Issue[] | undefined): Map<string, string> {
  const m = new Map<string, string>();
  for (const it of issues ?? []) m.set(it.path, it.message);
  return m;
}

/** 模板输入框:失焦时单行省略,聚焦时展开为等宽多行(spec「自定义类型」)。 */
function TemplateInput({
  value,
  onChange,
  placeholder,
  className,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  className?: string;
  "aria-label"?: string;
}) {
  const [focused, setFocused] = useState(false);
  if (focused) {
    return (
      <Textarea
        autoFocus
        rows={3}
        value={value}
        aria-label={ariaLabel}
        onChange={(e) => onChange(e.target.value)}
        onBlur={() => setFocused(false)}
        placeholder={placeholder}
        className={cn("font-mono text-[13px]", className)}
      />
    );
  }
  return (
    <Input
      value={value}
      aria-label={ariaLabel}
      onChange={(e) => onChange(e.target.value)}
      onFocus={() => setFocused(true)}
      placeholder={placeholder}
      className={cn("h-8 truncate font-mono text-[13px]", className)}
    />
  );
}

function RemoveButton({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      aria-label={label}
      className="text-muted-foreground hover:text-destructive"
      onClick={onClick}
    >
      <Trash2 className="size-3.5" />
    </Button>
  );
}

export interface CustomTypeEditorDialogProps {
  open: boolean;
  /** undefined = 新建;非 undefined = 编辑该 id。 */
  typeId?: number;
  onOpenChange: (open: boolean) => void;
  onSaved?: () => void;
}

export function CustomTypeEditorDialog({ open, typeId, onOpenChange, onSaved }: CustomTypeEditorDialogProps) {
  const { t } = useTranslation();
  const store = useCustomTypeStore();

  const [draft, setDraft] = useState<CustomTypeDraft>(emptyDraft());
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [issues, setIssues] = useState<custom_type_entity.Issue[]>([]);
  const [warnings, setWarnings] = useState<custom_type_entity.Issue[]>([]);
  const [usage, setUsage] = useState<number | null>(null);

  // 打开(或切换编辑目标)时从后端回填;渲染期对比上次值,避免 effect 里 setState
  // (AgentSourceDialog 同款模式,见 docs/DESIGN.md「Forms」)。
  const [prevSync, setPrevSync] = useState<{ open: boolean; typeId?: number }>({ open: false });
  if (open !== prevSync.open || typeId !== prevSync.typeId) {
    setPrevSync({ open, typeId });
    if (open) {
      setIssues([]);
      setWarnings([]);
      if (typeId === undefined) {
        setDraft(emptyDraft());
        setUsage(null);
      } else {
        setLoading(true);
        setUsage(null);
        void (async () => {
          try {
            const [ct, n] = await Promise.all([store.get(typeId), store.usage(typeId)]);
            setDraft(fromWire(ct));
            setUsage(n);
          } catch (e) {
            toast.error(String(e));
            onOpenChange(false);
          } finally {
            setLoading(false);
          }
        })();
      }
    }
  }

  const issueMap = buildIssueMap(issues);
  const isEdit = typeId !== undefined;

  const handleSave = async () => {
    setSaving(true);
    try {
      const res = await store.save(toWire(draft));
      if (res.issues && res.issues.length > 0) {
        setIssues(res.issues);
        setWarnings([]);
        return;
      }
      setIssues([]);
      setWarnings(res.warnings ?? []);
      notifySuccess(t("customType.saved"));
      onOpenChange(false);
      onSaved?.();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const updateField = (idx: number, patch: Partial<FieldDraft>) => {
    setDraft((d) => ({
      ...d,
      fields: d.fields.map((f, i) => (i === idx ? { ...f, ...patch } : f)),
    }));
  };

  const fieldsTab = (
    <div className="grid gap-2">
      {issueMap.get("fields") && <p className="text-xs text-destructive">{issueMap.get("fields")}</p>}
      {draft.fields.map((f, i) => (
        <div key={i} className="grid grid-cols-[1fr_1fr_1fr_120px_34px_44px_28px] items-start gap-2">
          <Field label={i === 0 ? t("customType.fieldName") : undefined}>
            <Input
              value={f.name}
              onChange={(e) => updateField(i, { name: e.target.value })}
              className="h-8 font-mono text-[13px]"
              aria-label={t("customType.fieldName")}
            />
            {issueMap.get(`fields[${i}].name`) && (
              <p className="text-xs text-destructive">{issueMap.get(`fields[${i}].name`)}</p>
            )}
          </Field>
          <Field label={i === 0 ? t("customType.fieldLabel") : undefined}>
            <Input
              value={f.label}
              onChange={(e) => updateField(i, { label: e.target.value })}
              className="h-8 text-[13px]"
              aria-label={t("customType.fieldLabel")}
            />
          </Field>
          <Field label={i === 0 ? t("customType.fieldPlaceholder") : undefined}>
            <Input
              value={f.placeholder}
              onChange={(e) => updateField(i, { placeholder: e.target.value })}
              className="h-8 text-[13px]"
              aria-label={t("customType.fieldPlaceholder")}
            />
          </Field>
          <Field label={i === 0 ? t("customType.fieldDefault") : undefined}>
            <Input
              value={f.default}
              disabled={f.secret}
              placeholder={f.secret ? t("customType.fieldSecretNoDefault") : undefined}
              onChange={(e) => updateField(i, { default: e.target.value })}
              className="h-8 font-mono text-[13px]"
              aria-label={t("customType.fieldDefault")}
            />
            {issueMap.get(`fields[${i}].default`) && (
              <p className="text-xs text-destructive">{issueMap.get(`fields[${i}].default`)}</p>
            )}
          </Field>
          <div className={cn("flex justify-center", i === 0 && "pt-[23px]")}>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={t("customType.fieldSecret")}
              aria-pressed={f.secret}
              className={cn(f.secret && "border border-primary/40 bg-primary/10 text-primary")}
              onClick={() => updateField(i, { secret: !f.secret, default: f.secret ? f.default : "" })}
            >
              {f.secret ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
            </Button>
          </div>
          <div className={cn("flex justify-center", i === 0 && "pt-[23px]")}>
            <Switch
              checked={f.required}
              onCheckedChange={(checked) => updateField(i, { required: checked })}
              aria-label={t("customType.fieldRequired")}
            />
          </div>
          <div className={cn(i === 0 && "pt-[15px]")}>
            <RemoveButton
              label={t("customType.removeField")}
              onClick={() => setDraft((d) => ({ ...d, fields: d.fields.filter((_, idx) => idx !== i) }))}
            />
          </div>
        </div>
      ))}
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="w-fit gap-1 px-2 text-xs text-primary hover:bg-primary/10 hover:text-primary"
        onClick={() => setDraft((d) => ({ ...d, fields: [...d.fields, emptyField()] }))}
      >
        <Plus className="size-3.5" />
        {t("customType.addField")}
      </Button>
    </div>
  );

  const updateAuth = (idx: number, patch: Partial<AuthDraft>) => {
    setDraft((d) => ({ ...d, httpAuth: d.httpAuth.map((a, i) => (i === idx ? { ...a, ...patch } : a)) }));
  };

  const requestTab = (
    <div className="grid gap-[22px]">
      {issueMap.get("http") && <p className="text-xs text-destructive">{issueMap.get("http")}</p>}
      <Field label={t("customType.baseUrl")} required>
        <TemplateInput
          value={draft.httpBaseUrl}
          onChange={(v) => setDraft((d) => ({ ...d, httpBaseUrl: v }))}
          placeholder="https://{{host}}"
          aria-label={t("customType.baseUrl")}
        />
        {issueMap.get("http.base_url") && <p className="text-xs text-destructive">{issueMap.get("http.base_url")}</p>}
        <p className="text-xs text-muted-foreground">{t("customType.baseUrlHint")}</p>
      </Field>
      <Field label={t("customType.auth")}>
        <div className="grid gap-1.5">
          {draft.httpAuth.map((a, i) => (
            <div key={i} className="grid gap-1.5 rounded-md border border-border p-2">
              <div className="flex items-center gap-2">
                <Select
                  value={a.type}
                  onValueChange={(v) =>
                    updateAuth(i, {
                      type: v,
                      name: AUTH_HAS_NAME[v] ? a.name : "",
                      values: Array.from({ length: AUTH_VALUE_COUNT[v] ?? 1 }, (_, j) => a.values[j] ?? ""),
                    })
                  }
                >
                  <SelectTrigger className="h-8 w-[140px] text-[13px]">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {AUTH_KINDS.map((kind) => (
                      <SelectItem key={kind} value={kind}>
                        {t(`customType.authType.${kind}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {AUTH_HAS_NAME[a.type] && (
                  <Input
                    value={a.name}
                    onChange={(e) => updateAuth(i, { name: e.target.value })}
                    placeholder={t("customType.authName")}
                    className="h-8 flex-1 font-mono text-[13px]"
                  />
                )}
                <div className="ml-auto">
                  <RemoveButton
                    label={t("customType.removeAuth")}
                    onClick={() => setDraft((d) => ({ ...d, httpAuth: d.httpAuth.filter((_, idx) => idx !== i) }))}
                  />
                </div>
              </div>
              {issueMap.get(`http.auth[${i}].type`) && (
                <p className="text-xs text-destructive">{issueMap.get(`http.auth[${i}].type`)}</p>
              )}
              {issueMap.get(`http.auth[${i}].name`) && (
                <p className="text-xs text-destructive">{issueMap.get(`http.auth[${i}].name`)}</p>
              )}
              {a.values.map((v, j) => (
                <div key={j}>
                  <TemplateInput
                    value={v}
                    onChange={(nv) => updateAuth(i, { values: a.values.map((ov, oj) => (oj === j ? nv : ov)) })}
                    placeholder={
                      a.type === "basic"
                        ? j === 0
                          ? t("customType.authUsername")
                          : t("customType.authPassword")
                        : t("customType.authValue")
                    }
                  />
                  {issueMap.get(`http.auth[${i}].values`) && j === 0 && (
                    <p className="text-xs text-destructive">{issueMap.get(`http.auth[${i}].values`)}</p>
                  )}
                  {issueMap.get(`http.auth[${i}].values[${j}]`) && (
                    <p className="text-xs text-destructive">{issueMap.get(`http.auth[${i}].values[${j}]`)}</p>
                  )}
                </div>
              ))}
            </div>
          ))}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="w-fit gap-1 px-2 text-xs text-primary hover:bg-primary/10 hover:text-primary"
            onClick={() =>
              setDraft((d) => ({
                ...d,
                httpAuth: [...d.httpAuth, { type: "header", name: "", values: [""] }],
              }))
            }
          >
            <Plus className="size-3.5" />
            {t("customType.addAuth")}
          </Button>
        </div>
      </Field>
    </div>
  );

  const updateEnv = (idx: number, patch: Partial<EnvDraft>) => {
    setDraft((d) => ({ ...d, commandEnv: d.commandEnv.map((e, i) => (i === idx ? { ...e, ...patch } : e)) }));
  };

  const commandTab = (
    <div className="grid gap-[22px]">
      {issueMap.get("command") && <p className="text-xs text-destructive">{issueMap.get("command")}</p>}
      <Field label={t("customType.commandTemplate")}>
        <TemplateInput
          value={draft.commandTemplate}
          onChange={(v) => setDraft((d) => ({ ...d, commandTemplate: v }))}
          placeholder={t("customType.commandTemplatePlaceholder")}
          aria-label={t("customType.commandTemplate")}
        />
        {issueMap.get("command.template") && (
          <p className="text-xs text-destructive">{issueMap.get("command.template")}</p>
        )}
        <p className="text-xs text-muted-foreground">{t("customType.commandTemplateHint")}</p>
        {warnings.some((w) => w.path === "command.template") && (
          <p className="flex items-start gap-1.5 text-xs text-warning">
            <Info className="mt-0.5 size-3.5 shrink-0" />
            {warnings.find((w) => w.path === "command.template")?.message}
          </p>
        )}
      </Field>
      <Field label={t("customType.env")}>
        <div className="grid gap-1.5">
          {draft.commandEnv.map((e, i) => (
            <div key={i}>
              <div className="grid grid-cols-[220px_14px_minmax(0,1fr)_28px] items-center gap-2">
                <Input
                  value={e.name}
                  onChange={(ev) => updateEnv(i, { name: ev.target.value })}
                  className="h-8 font-mono text-[13px]"
                  aria-label={t("customType.envName")}
                />
                <span className="text-center text-muted-foreground">=</span>
                <TemplateInput
                  value={e.value}
                  onChange={(v) => updateEnv(i, { value: v })}
                  aria-label={t("customType.envValue")}
                />
                <RemoveButton
                  label={t("customType.removeEnv")}
                  onClick={() => setDraft((d) => ({ ...d, commandEnv: d.commandEnv.filter((_, idx) => idx !== i) }))}
                />
              </div>
              {issueMap.get(`command.env[${i}].name`) && (
                <p className="text-xs text-destructive">{issueMap.get(`command.env[${i}].name`)}</p>
              )}
              {issueMap.get(`command.env[${i}].value`) && (
                <p className="text-xs text-destructive">{issueMap.get(`command.env[${i}].value`)}</p>
              )}
            </div>
          ))}
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="w-fit gap-1 px-2 text-xs text-primary hover:bg-primary/10 hover:text-primary"
              onClick={() => setDraft((d) => ({ ...d, commandEnv: [...d.commandEnv, { name: "", value: "" }] }))}
            >
              <Plus className="size-3.5" />
              {t("customType.addEnv")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 w-fit px-2 text-xs text-muted-foreground"
              onClick={() =>
                setDraft((d) => {
                  const existing = new Set(d.commandEnv.map((e) => e.name));
                  const generated = d.fields
                    .filter((f) => f.name && !existing.has(f.name.toUpperCase()))
                    .map((f) => ({ name: f.name.toUpperCase(), value: `{{${f.name}}}` }));
                  return { ...d, commandEnv: [...d.commandEnv, ...generated] };
                })
              }
            >
              {t("customType.envFromFields")}
            </Button>
          </div>
        </div>
      </Field>
    </div>
  );

  const notesTab = (
    <div className="grid gap-2">
      <Textarea
        rows={14}
        value={draft.usage}
        onChange={(e) => setDraft((d) => ({ ...d, usage: e.target.value }))}
        className="font-mono text-[12.5px] leading-relaxed"
        aria-label={t("customType.notes")}
      />
      <p className="text-xs text-muted-foreground">{t("customType.notesHint")}</p>
    </div>
  );

  const policyTab = (
    <div className="grid gap-3">
      <PolicyTagEditor
        label={t("customType.policyAllow")}
        items={draft.allowList}
        variant="allow"
        placeholder={
          draft.execMode === "http" ? t("customType.policyPlaceholderHttp") : t("customType.policyPlaceholderCommand")
        }
        onAdd={(vals) => setDraft((d) => ({ ...d, allowList: [...d.allowList, ...vals] }))}
        onRemove={(idx) => setDraft((d) => ({ ...d, allowList: d.allowList.filter((_, i) => i !== idx) }))}
      />
      <PolicyTagEditor
        label={t("customType.policyDeny")}
        items={draft.denyList}
        variant="deny"
        placeholder={
          draft.execMode === "http" ? t("customType.policyPlaceholderHttp") : t("customType.policyPlaceholderCommand")
        }
        onAdd={(vals) => setDraft((d) => ({ ...d, denyList: [...d.denyList, ...vals] }))}
        onRemove={(idx) => setDraft((d) => ({ ...d, denyList: d.denyList.filter((_, i) => i !== idx) }))}
      />
      <div className="flex items-start gap-2 rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
        <Info className="mt-0.5 size-3.5 shrink-0" />
        {t("customType.policyHint")}
      </div>
    </div>
  );

  const groups: ConfigGroup[] = [
    { key: "fields", label: "customType.tabFields", render: () => fieldsTab },
    {
      key: "request",
      label: draft.execMode === "http" ? "customType.tabRequest" : "customType.tabCommand",
      render: () => (draft.execMode === "http" ? requestTab : commandTab),
    },
    { key: "notes", label: "customType.tabNotes", render: () => notesTab },
    { key: "policy", label: "customType.tabPolicy", render: () => policyTab },
  ];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="lg" className="flex max-h-[85vh] flex-col">
        <DialogHeader>
          <DialogTitle>
            {isEdit ? t("customType.editorTitleEdit", { name: draft.name }) : t("customType.editorTitleNew")}
          </DialogTitle>
          <DialogDescription>{t("customType.editorDesc")}</DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex flex-1 items-center justify-center py-10">
            <Loader2 className="size-5 animate-spin text-primary" />
          </div>
        ) : (
          <div className="min-h-0 flex-1 space-y-5 overflow-y-auto pr-1">
            <div className="flex items-end gap-[14px]">
              <Field label={t("customType.icon")} className="w-14 shrink-0">
                <IconPicker value={draft.icon} onChange={(icon) => setDraft((d) => ({ ...d, icon }))} compact />
              </Field>
              <Field label={t("customType.name")} required className="min-w-0 flex-1">
                <Input value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} />
                {issueMap.get("name") && <p className="text-xs text-destructive">{issueMap.get("name")}</p>}
              </Field>
              <Field label={t("customType.slug")} required className="w-[220px] shrink-0">
                <Input
                  value={draft.slug}
                  disabled={isEdit}
                  onChange={(e) => setDraft((d) => ({ ...d, slug: e.target.value }))}
                  className="font-mono text-[13px]"
                />
                {isEdit ? (
                  <p className="text-xs text-muted-foreground">{t("customType.slugImmutable")}</p>
                ) : (
                  issueMap.get("slug") && <p className="text-xs text-destructive">{issueMap.get("slug")}</p>
                )}
              </Field>
            </div>

            <Field label={t("customType.execMode")}>
              <Segmented
                value={draft.execMode}
                onChange={(v) => setDraft((d) => ({ ...d, execMode: v }))}
                options={[
                  { value: "http", label: t("customType.execModeHttp"), icon: Globe },
                  { value: "command", label: t("customType.execModeCommand"), icon: SquareTerminal },
                ]}
              />
              {issueMap.get("exec_mode") && <p className="text-xs text-destructive">{issueMap.get("exec_mode")}</p>}
            </Field>

            <ConfigTabs groups={groups} />
          </div>
        )}

        <DialogFooter className="items-center sm:justify-between">
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Info className="size-3.5" />
            {isEdit ? t("customType.usageFooter", { count: usage ?? 0 }) : t("customType.usageFooterNew")}
          </span>
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
              {t("action.cancel")}
            </Button>
            <Button onClick={() => void handleSave()} disabled={saving || loading}>
              {saving && <Loader2 className="size-4 animate-spin" />}
              {t("action.save")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
