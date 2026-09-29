import { useEffect, useMemo, useState, type Ref } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { AlertCircle, Info, Loader2 } from "lucide-react";
import { Button, Input } from "@opskat/ui";
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";
import { Fields, type FieldDesc } from "@/components/asset/configFields";
import { Field, FieldLabel } from "@/components/asset/fields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { useAssetCredential } from "@/components/asset/useAssetCredential";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import type { asset_entity, custom_type_entity } from "../../../wailsjs/go/models";
import { RevealGenericSecret } from "../../../wailsjs/go/customtype/CustomType";
import { proxyChainValidationKey, resolveSaveProxyChainSecrets } from "./proxyConfig";
import { tlsFileFields } from "./tlsFields";
import {
  EXEC_MODE_HTTP,
  buildGenericConfig,
  buildGenericTestInput,
  genericMissingFields,
  genericTunnelId,
  initGenericState,
  parseGenericConfig,
  type GenericConfigJSON,
  type GenericFormState,
  type GenericSecretState,
} from "./GenericConfigSection.config";

/** 通用资产的配置区:按所选自定义类型的字段结构生成输入;HTTP 方式另有连接页。 */
export function GenericConfigSection({ ref, editAsset, variant, onValidityChange }: ConfigSectionProps) {
  const { t } = useTranslation();
  const editConfig = useMemo(() => (editAsset ? parseGenericConfig(editAsset.Config) : undefined), [editAsset]);
  const slug = editConfig?.custom_type ?? variant ?? "";

  const types = useCustomTypeStore((s) => s.types);
  const loaded = useCustomTypeStore((s) => s.loaded);
  const loading = useCustomTypeStore((s) => s.loading);
  const load = useCustomTypeStore((s) => s.load);
  const getType = useCustomTypeStore((s) => s.get);

  const [didLoad, setDidLoad] = useState(false);
  if (!didLoad && !loaded && !loading) {
    setDidLoad(true);
    load().catch((e) => toast.error(String(e)));
  }

  const summary = types.find((ct) => ct.slug === slug);
  const [reloadKey, setReloadKey] = useState(0);
  const [fetched, setFetched] = useState<{ ct?: custom_type_entity.CustomType; error?: string; reloadKey: number }>();

  // 类型结构未就绪前壳不能保存 / 测试(切换类型时壳里还留着上一个 section 的有效性)。
  useEffect(() => {
    onValidityChange({ canTest: false, canSave: false });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const typeId = summary?.id;
  useEffect(() => {
    if (typeId === undefined) return;
    let cancelled = false;
    getType(typeId)
      .then((ct) => {
        if (!cancelled) setFetched({ ct, reloadKey });
      })
      .catch((e) => {
        if (!cancelled) setFetched({ error: String(e), reloadKey });
      });
    return () => {
      cancelled = true;
    };
  }, [typeId, reloadKey, getType]);

  if (fetched?.error) {
    return <InlineError text={fetched.error} />;
  }
  if (loaded && !summary) {
    return <InlineError text={t("asset.generic.typeNotFound", { slug })} />;
  }
  if (!fetched?.ct || fetched.ct.id !== typeId) {
    return (
      <div className="flex items-center justify-center py-6 text-muted-foreground">
        <Loader2 className="size-5 animate-spin text-primary" />
      </div>
    );
  }
  return (
    <GenericTypeForm
      key={`${fetched.ct.id}:${fetched.reloadKey}`}
      ref={ref}
      ct={fetched.ct}
      editAsset={editAsset}
      editConfig={editConfig}
      onValidityChange={onValidityChange}
      onTypeEdited={() => setReloadKey((k) => k + 1)}
    />
  );
}

function InlineError({ text }: { text: string }) {
  return (
    <p className="flex items-center gap-1.5 text-xs text-destructive">
      <AlertCircle className="h-3.5 w-3.5 shrink-0" />
      {text}
    </p>
  );
}

interface GenericTypeFormProps {
  ref?: Ref<AssetFormHandle>;
  ct: custom_type_entity.CustomType;
  editAsset?: asset_entity.Asset;
  editConfig?: GenericConfigJSON;
  onValidityChange: ConfigSectionProps["onValidityChange"];
  onTypeEdited: () => void;
}

function GenericTypeForm({ ref, ct, editAsset, editConfig, onValidityChange, onTypeEdited }: GenericTypeFormProps) {
  const { t } = useTranslation();
  const isHttp = ct.execMode === EXEC_MODE_HTTP;
  const { managedPasswords } = useAssetCredential();
  const [touched, setTouched] = useState<Record<string, boolean>>({});
  const [editorOpen, setEditorOpen] = useState(false);

  const { state, patch, setState } = useConfigSection<GenericFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: () => initGenericState(ct, editConfig, editAsset?.sshTunnelId || 0),
    validate: (s) => {
      const missing = genericMissingFields(ct, s);
      const chainError = isHttp ? proxyChainValidationKey(s.proxyChainLayers) : "";
      const ok = missing.length === 0 && !chainError;
      return {
        canTest: ok && isHttp,
        canSave: ok,
        testable: isHttp,
        saveDisabledReason: missing.length > 0 ? "asset.generic.missingRequired" : chainError,
      };
    },
    build: async (s, c) => ({
      configJSON: await buildGenericConfig(
        ct,
        s,
        c.encryptPassword,
        await resolveSaveProxyChainSecrets(s.proxyChainLayers, c.encryptPassword)
      ),
      sshTunnelId: genericTunnelId(ct, s),
    }),
    buildTest: isHttp
      ? async (s) => ({ assetType: "generic", configJSON: buildGenericTestInput(ct, s, editAsset?.ID), password: "" })
      : undefined,
    deps: [ct],
    validityDeps: [ct],
  });

  const missing = new Set(genericMissingFields(ct, state));
  // 编辑态直接标出缺值(常见入口是详情页的「去填写」);新建时等用户动过该字段再标。
  const showRequired = (name: string) => missing.has(name) && (touched[name] || !!editAsset);
  const touch = (name: string) => setTouched((m) => (m[name] ? m : { ...m, [name]: true }));

  const setValue = (name: string, value: string) => {
    touch(name);
    setState((s) => ({
      ...s,
      values: { ...s.values, [name]: value },
      explicit: { ...s.explicit, [name]: true },
    }));
  };
  const setSecret = (name: string, p: Partial<GenericSecretState>) => {
    touch(name);
    setState((s) => ({ ...s, secrets: { ...s.secrets, [name]: { ...s.secrets[name], ...p } } }));
  };

  const requiredHint = (name: string) =>
    showRequired(name) ? (
      <p data-testid={`generic-required-${name}`} className="flex items-center gap-1 text-xs text-destructive">
        <AlertCircle className="h-3 w-3 shrink-0" />
        {t("asset.generic.required")}
      </p>
    ) : null;

  const renderFields = () => (
    <div className="flex flex-col gap-4">
      {(ct.fields ?? []).map((f) => {
        const label = f.label || f.name;
        if (f.secret) {
          const s = state.secrets[f.name];
          return (
            <div key={f.name} data-testid={`generic-secret-${f.name}`} className="flex flex-col gap-[7px]">
              <PasswordSourceField
                sourceLabel={label}
                required={f.required}
                source={s.source}
                onSourceChange={(source) => setSecret(f.name, { source })}
                password={s.password}
                onPasswordChange={(password) => setSecret(f.name, { password })}
                credentialId={s.credentialId}
                onCredentialIdChange={(credentialId) => setSecret(f.name, { credentialId })}
                managedPasswords={managedPasswords}
                placeholder={f.placeholder}
                hasExistingPassword={!!s.storedCipher}
                revealExisting={editAsset?.ID ? () => RevealGenericSecret(editAsset.ID, f.name) : undefined}
                secretLabel={t("asset.generic.secretValue")}
              />
              {requiredHint(f.name)}
            </div>
          );
        }
        return (
          <Field key={f.name} label={label} required={f.required}>
            <Input
              data-testid={`generic-field-${f.name}`}
              value={state.values[f.name] ?? ""}
              placeholder={f.placeholder}
              aria-invalid={showRequired(f.name) || undefined}
              onChange={(e) => setValue(f.name, e.target.value)}
              onBlur={() => touch(f.name)}
            />
            {requiredHint(f.name)}
          </Field>
        );
      })}
      <p data-testid="generic-type-hint" className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
        <Info className="h-3.5 w-3.5 shrink-0" />
        <span>{t("asset.generic.fromType")}</span>
        <span className="font-medium text-foreground">{ct.name}</span>
        <Button
          type="button"
          variant="link"
          size="xs"
          className="h-auto px-1"
          data-testid="generic-edit-type"
          onClick={() => setEditorOpen(true)}
        >
          {t("asset.generic.editType")}
        </Button>
      </p>
    </div>
  );

  const connectionFields: FieldDesc<GenericFormState>[] = [
    { kind: "tunnel", excludeIds: editAsset?.ID ? [editAsset.ID] : undefined },
    {
      kind: "custom",
      render: () => (
        <div className="border-t border-border pt-4">
          <FieldLabel>{t("asset.tabTls")}</FieldLabel>
        </div>
      ),
    },
    ...tlsFileFields<GenericFormState>({ serverNamePlaceholder: "" }),
  ];

  const groups: ConfigGroup[] = [{ key: "config", label: "asset.generic.tabConfig", render: renderFields }];
  if (isHttp) {
    groups.push({
      key: "connection",
      label: "asset.tabConnection",
      render: () => <Fields fields={connectionFields} state={state} patch={patch} />,
    });
  }

  return (
    <>
      <ConfigTabs groups={groups} />
      <CustomTypeEditorDialog open={editorOpen} typeId={ct.id} onOpenChange={setEditorOpen} onSaved={onTypeEdited} />
    </>
  );
}
