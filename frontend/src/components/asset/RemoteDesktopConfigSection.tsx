import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema, type FieldDesc } from "@/components/asset/configFields";
import { Field } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey } from "./proxyConfig";
import { useAssetCredential } from "./useAssetCredential";
import {
  buildRemoteDesktopConfig,
  parseRemoteDesktopConfig,
  parseRemoteDesktopPasswordCredentialConfig,
  RDP_DEFAULTS,
  VNC_DEFAULTS,
  type RemoteDesktopFormState,
} from "./RemoteDesktopConfigSection.config";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";

function createRemoteDesktopSection(type: "vnc" | "rdp") {
  function RemoteDesktopConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
    const { t } = useTranslation();
    const passwordCredentialConfig = useMemo(
      () => (editAsset ? parseRemoteDesktopPasswordCredentialConfig(editAsset.Config) : undefined),
      [editAsset]
    );
    const cred = useAssetCredential(editAsset, passwordCredentialConfig);
    const defaults = type === "rdp" ? RDP_DEFAULTS : VNC_DEFAULTS;

    const { state, patch } = useConfigSection<RemoteDesktopFormState>({
      ref,
      editAsset,
      onValidityChange,
      init: (a) => (a ? parseRemoteDesktopConfig(a.Config, type) : { ...defaults }),
      validate: (s) => {
        const baseOk = !!s.host.trim() && s.port > 0 && s.port <= 65535 && (type !== "rdp" || !!s.username.trim());
        const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
        const canUse = baseOk && !proxyChainError;
        return {
          canTest: canUse,
          canSave: canUse,
          saveDisabledReason: baseOk
            ? proxyChainError
            : type === "rdp"
              ? "asset.formMissingHostOrUsername"
              : "asset.formMissingHost",
        };
      },
      build: async (s, ctx) => ({
        configJSON: await buildRemoteDesktopConfig(
          s,
          type,
          await resolveSaveCredential(cred.value, ctx.encryptPassword),
          ctx.encryptPassword
        ),
        sshTunnelId: 0,
      }),
      buildTest: async (s) => {
        const plainPassword = cred.value.password || s.password;
        const cfg = JSON.parse(
          await buildRemoteDesktopConfig(s, type, resolveTestCredential(cred.value), async (plain) => plain)
        );
        if (plainPassword) cfg.password = plainPassword;
        return {
          assetType: type,
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
                placeholder: type === "rdp" ? "windows.example.com" : "vnc.example.com",
                width: "flex-1",
                testid: `${type}-host-input`,
              },
              {
                kind: "number",
                key: "port",
                label: "asset.port",
                width: "w-[110px] shrink-0",
                placeholder: String(defaults.port),
                testid: `${type}-port-input`,
              },
            ],
          },
          { kind: "text", key: "username", label: "asset.username", testid: `${type}-username-input` },
          { kind: "password", placeholder: "asset.passwordPlaceholder" },
          ...(type === "rdp"
            ? ([
                {
                  kind: "text",
                  key: "domain",
                  label: "asset.domain",
                  placeholder: "DOMAIN",
                  testid: "rdp-domain-input",
                },
                {
                  kind: "row",
                  fields: [
                    { kind: "number", key: "screenWidth", label: "remoteDesktop.width", width: "flex-1" },
                    { kind: "number", key: "screenHeight", label: "remoteDesktop.height", width: "flex-1" },
                    { kind: "number", key: "colorDepth", label: "remoteDesktop.colorDepth", width: "flex-1" },
                  ],
                },
                { kind: "switch", key: "ignoreCert", label: "remoteDesktop.ignoreCert" },
              ] as FieldDesc<RemoteDesktopFormState>[])
            : ([
                {
                  kind: "text",
                  key: "securityType",
                  label: "remoteDesktop.securityType",
                  placeholder: "any",
                  testid: "vnc-security-type-input",
                },
              ] as FieldDesc<RemoteDesktopFormState>[])),
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
                  testId={`${type}-file-ssh-select`}
                />
              </Field>
            ),
          },
        ],
      },
    ];

    return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
  }

  return RemoteDesktopConfigSection;
}

export const VNCConfigSection = createRemoteDesktopSection("vnc");
export const RDPConfigSection = createRemoteDesktopSection("rdp");
