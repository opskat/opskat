import { forwardRef, useMemo } from "react";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildRDPConfig,
  parseRDPConfig,
  parseRDPCredentialConfig,
  RDP_DEFAULTS,
  type RDPFormState,
} from "./RDPConfigSection.config";

export const RDPConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function RDPConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const passwordCredentialConfig = useMemo(
    () => (editAsset ? parseRDPCredentialConfig(editAsset.Config) : undefined),
    [editAsset]
  );
  const cred = useAssetCredential(editAsset, passwordCredentialConfig);

  const { state, patch } = useConfigSection<RDPFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseRDPConfig(a.Config) : { ...RDP_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.host.trim() && !!s.username.trim() && s.port > 0;
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" };
    },
    build: async (s, ctx) => ({
      configJSON: buildRDPConfig(s, await resolveSaveCredential(cred.value, ctx.encryptPassword)),
      sshTunnelId: 0,
    }),
    buildTest: async (s) => ({
      assetType: "rdp",
      configJSON: buildRDPConfig(s, resolveTestCredential(cred.value)),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups: ConfigGroupSchema<RDPFormState>[] = [
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
              placeholder: "192.168.1.10",
              width: "flex-1",
              testid: "rdp-host-input",
            },
            {
              kind: "number",
              key: "port",
              label: "asset.port",
              width: "w-[110px] shrink-0",
              blankWhenZero: true,
              placeholder: "3389",
              testid: "rdp-port-input",
            },
          ],
        },
        {
          kind: "row",
          fields: [
            {
              kind: "text",
              key: "username",
              label: "asset.username",
              required: true,
              width: "flex-1",
              testid: "rdp-username-input",
            },
            {
              kind: "text",
              key: "domain",
              label: "asset.rdpDomain",
              width: "w-[180px] shrink-0",
              testid: "rdp-domain-input",
            },
          ],
        },
        { kind: "password", placeholder: "asset.passwordPlaceholder" },
      ],
    },
    {
      key: "display",
      label: "asset.rdpDisplay",
      fields: [
        {
          kind: "row",
          fields: [
            { kind: "number", key: "width", label: "asset.rdpWidth", min: 640, width: "flex-1", testid: "rdp-width-input" },
            { kind: "number", key: "height", label: "asset.rdpHeight", min: 480, width: "flex-1", testid: "rdp-height-input" },
          ],
        },
        { kind: "note", text: "asset.rdpResolutionHint" },
        { kind: "switch", key: "clipboard", label: "asset.rdpClipboardSync", description: "asset.rdpClipboardHint" },
      ],
    },
  ];

  return (
    <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />
  );
});
