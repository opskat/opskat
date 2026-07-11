import { useTranslation } from "react-i18next";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildOSSConfig,
  parseOSSConfig,
  providerPrefillPatch,
  ossCredentialFragment,
  OSS_DEFAULTS,
  OSS_PROVIDER_VALUES,
  OSS_PROVIDER_LABEL_KEYS,
  type OSSFormState,
} from "./OSSConfigSection.config";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";

/** 厂商下拉:选中即触发智能预填(endpoint/region/path-style)。逻辑走 providerPrefillPatch 纯函数,
 *  不在共享 configFields 里按厂商字符串分支(满足 OCP:扩展靠注册/纯函数,不改分发器)。 */
function ProviderField({ state, patch }: { state: OSSFormState; patch: (p: Partial<OSSFormState>) => void }) {
  const { t } = useTranslation();
  return (
    <Field label={t("oss.form.provider")}>
      <Select value={state.provider} onValueChange={(v) => patch(providerPrefillPatch(v))}>
        <SelectTrigger data-testid="oss-provider-select" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {OSS_PROVIDER_VALUES.map((v) => (
            <SelectItem key={v} value={v}>
              {t(OSS_PROVIDER_LABEL_KEYS[v])}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}

const OSS_GROUPS: ConfigGroupSchema<OSSFormState>[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    fields: [
      { kind: "custom", render: (s, patch) => <ProviderField state={s} patch={patch} /> },
      {
        kind: "text",
        key: "endpoint",
        label: "oss.form.endpoint",
        required: true,
        placeholder: "oss.form.endpointPlaceholder",
      },
      { kind: "text", key: "region", label: "oss.form.region", placeholder: "oss.form.regionPlaceholder" },
      { kind: "text", key: "accessKeyId", label: "oss.form.accessKeyId" },
      { kind: "password", usernameKey: "accessKeyId", secretLabel: "oss.form.secretAccessKey" },
    ],
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    fields: [
      { kind: "switch", key: "usePathStyle", label: "oss.form.usePathStyle" },
      { kind: "switch", key: "useSSL", label: "oss.form.useSSL" },
      { kind: "switch", key: "skipTLSVerify", label: "oss.form.skipTLSVerify" },
      { kind: "number", key: "connectTimeout", label: "oss.form.connectTimeout", min: 0, blankWhenZero: true },
      { kind: "number", key: "partSizeMB", label: "oss.form.partSizeMB", min: 0, blankWhenZero: true },
    ],
  },
];

export function OSSConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  // OSS 机密键是 secret_access_key(非通用 password),编辑态须显式映射,否则 inline 密文回填/保存会丢。
  const cred = useAssetCredential(editAsset, editAsset ? ossCredentialFragment(editAsset.Config) : undefined);
  const { state, patch } = useConfigSection<OSSFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseOSSConfig(a.Config) : { ...OSS_DEFAULTS }),
    validate: (s) => {
      const ok = s.endpoint.trim() !== "" && s.accessKeyId.trim() !== "";
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "oss.error.required" };
    },
    build: async (s, ctx) => ({
      configJSON: buildOSSConfig(s, await resolveSaveCredential(cred.value, ctx.encryptPassword)),
      sshTunnelId: 0,
    }),
    buildTest: async (s) => ({
      assetType: "oss",
      configJSON: buildOSSConfig(s, resolveTestCredential(cred.value)),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(OSS_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
}
