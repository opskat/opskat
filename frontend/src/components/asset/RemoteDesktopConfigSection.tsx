import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { Field } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey } from "./proxyConfig";
import { useAssetCredential } from "./useAssetCredential";
import {
  buildRemoteDesktopConfig,
  parseRemoteDesktopConfig,
  parseRemoteDesktopPasswordCredentialConfig,
  VNC_DEFAULTS,
  type RemoteDesktopFormState,
} from "./RemoteDesktopConfigSection.config";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";

export function VNCConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  const { t } = useTranslation();
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseRemoteDesktopPasswordCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const { state, patch } = useConfigSection<RemoteDesktopFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseRemoteDesktopConfig(a.Config) : { ...VNC_DEFAULTS }),
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
      configJSON: await buildRemoteDesktopConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        ctx.encryptPassword
      ),
      sshTunnelId: 0,
    }),
    buildTest: async (s) => {
      const plainPassword = cred.value.password || s.password;
      const cfg = JSON.parse(
        await buildRemoteDesktopConfig(s, resolveTestCredential(cred.value), async (plain) => plain)
      );
      if (plainPassword) cfg.password = plainPassword;
      return {
        assetType: "vnc",
        configJSON: JSON.stringify(cfg),
        password: plainPassword,
      };
    },
    deps: [cred.value],
  });

  const groups: ConfigGroupSchema<RemoteDesktopFormState>[] = [
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
        {
          kind: "text",
          key: "securityType",
          label: "remoteDesktop.securityType",
          placeholder: "any",
          testid: "vnc-security-type-input",
        },
      ],
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      fields: [{ kind: "tunnel", tunnelOptionLabelKey: "asset.connectionTunnelProxy" }],
    },
    {
      key: "files",
      label: "remoteDesktop.files",
      fields: [
        {
          kind: "custom",
          render: () => (
            <Field label={t("remoteDesktop.fileSshAsset")}>
              <AssetSelect
                value={state.fileSshAssetId}
                onValueChange={(fileSshAssetId) => patch({ fileSshAssetId })}
                filterType="ssh"
                placeholder={t("remoteDesktop.fileSshAssetPlaceholder")}
                testId="vnc-file-ssh-select"
              />
            </Field>
          ),
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
}
