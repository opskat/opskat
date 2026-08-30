import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { Field } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey } from "./proxyConfig";
import { useAssetCredential } from "./useAssetCredential";
import {
  buildVNCConfig,
  parseVNCConfig,
  parseVNCPasswordCredentialConfig,
  VNC_DEFAULTS,
  type VNCFormState,
} from "./VNCConfigSection.config";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { VNC_ENCRYPTION_POLICIES, vncEncryptionLabelKey, type VNCEncryptionPolicy } from "@/lib/vncSecurity";

const VNC_ENCRYPTION_HINT_KEYS: Partial<Record<VNCEncryptionPolicy, string>> = {
  prefer_on: "vnc.encryptionPreferOnHint",
  prefer_off: "vnc.encryptionPreferOffHint",
};

export function VNCConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  const { t } = useTranslation();
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseVNCPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const { state, patch } = useConfigSection<VNCFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseVNCConfig(a.Config) : { ...VNC_DEFAULTS }),
    validate: (s) => {
      const baseOk = !!s.host.trim() && s.port > 0 && s.port <= 65535;
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      const canUse = baseOk && !proxyChainError;
      return {
        canTest: canUse,
        canSave: canUse,
        saveDisabledReason: baseOk ? proxyChainError : "asset.formMissingHost",
      };
    },
    build: async (s, ctx) => ({
      configJSON: await buildVNCConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        ctx.encryptPassword
      ),
      sshTunnelId: 0,
    }),
    buildTest: async (s) => {
      const plainPassword = cred.value.password || s.password;
      const cfg = JSON.parse(await buildVNCConfig(s, resolveTestCredential(cred.value), async (plain) => plain));
      if (plainPassword) cfg.password = plainPassword;
      return {
        assetType: "vnc",
        configJSON: JSON.stringify(cfg),
        password: plainPassword,
      };
    },
    deps: [cred.value],
  });

  const groups: ConfigGroupSchema<VNCFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "row",
          fields: [
            {
              kind: "text",
              key: "host",
              label: "asset.host",
              required: true,
              placeholder: "vnc.example.com",
              width: "flex-1",
              testid: "vnc-host-input",
            },
            {
              kind: "number",
              key: "port",
              label: "asset.port",
              width: "w-[110px] shrink-0",
              placeholder: String(VNC_DEFAULTS.port),
              testid: "vnc-port-input",
            },
          ],
        },
        { kind: "text", key: "username", label: "asset.username", testid: "vnc-username-input" },
        { kind: "password", placeholder: "asset.passwordPlaceholder" },
      ],
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      fields: [{ kind: "tunnel" }],
    },
    {
      key: "files",
      label: "vnc.files",
      fields: [
        {
          kind: "custom",
          render: () => (
            <Field label={t("vnc.fileSshAsset")}>
              <AssetSelect
                value={state.fileSshAssetId}
                onValueChange={(fileSshAssetId) => patch({ fileSshAssetId })}
                filterType="ssh"
                placeholder={t("vnc.fileSshAssetPlaceholder")}
                testId="vnc-file-ssh-select"
              />
            </Field>
          ),
        },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        {
          kind: "custom",
          render: () => {
            const selectedHint = VNC_ENCRYPTION_HINT_KEYS[state.encryption];
            return (
              <Field label={t("vnc.encryptionPolicy")}>
                <Select
                  value={state.encryption}
                  onValueChange={(encryption) => patch({ encryption: encryption as VNCEncryptionPolicy })}
                >
                  <SelectTrigger data-testid="vnc-encryption-select" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {VNC_ENCRYPTION_POLICIES.map((policy) => (
                      <SelectItem key={policy} value={policy}>
                        {t(vncEncryptionLabelKey(policy))}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {selectedHint && <p className="text-xs text-muted-foreground">{t(selectedHint)}</p>}
              </Field>
            );
          },
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
}
