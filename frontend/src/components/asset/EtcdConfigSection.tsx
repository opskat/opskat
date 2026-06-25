import { forwardRef } from "react";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import {
  buildEtcdConfig,
  parseEtcdConfig,
  parseEtcdEndpoints,
  ETCD_DEFAULTS,
  type EtcdFormState,
} from "./EtcdConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

const ETCD_GROUPS: ConfigGroupSchema<EtcdFormState>[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    fields: [
      {
        kind: "textarea",
        key: "endpoints",
        label: "etcd.form.endpoints",
        required: true,
        mono: true,
        rows: 3,
        placeholder: "10.0.0.1:2379\n10.0.0.2:2379",
        hint: "etcd.form.endpointsHint",
      },
      { kind: "text", key: "username", label: "asset.username" },
      { kind: "password" },
      {
        kind: "row",
        fields: [
          { kind: "number", key: "dialTimeoutSeconds", label: "etcd.form.dialTimeout", min: 0, width: "flex-1" },
          { kind: "number", key: "commandTimeoutSeconds", label: "etcd.form.commandTimeout", min: 0, width: "flex-1" },
        ],
      },
    ],
  },
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  {
    key: "tls",
    label: "asset.tabTls",
    fields: [
      { kind: "switch", key: "tls", label: "asset.tls" },
      {
        kind: "switch",
        key: "tlsInsecure",
        label: "etcd.form.tlsInsecure",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsServerName",
        label: "etcd.form.tlsServerName",
        placeholder: "etcd.example.com",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsCAFile",
        label: "etcd.form.tlsCAFile",
        placeholder: "/path/to/ca.pem",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsCertFile",
        label: "etcd.form.tlsCertFile",
        placeholder: "/path/to/client.crt",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsKeyFile",
        label: "etcd.form.tlsKeyFile",
        placeholder: "/path/to/client.key",
        visibleWhen: (s) => s.tls,
      },
    ],
  },
];

export const EtcdConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function EtcdConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const cred = useAssetCredential(editAsset);
  const { state, patch } = useConfigSection<EtcdFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseEtcdConfig(a.Config, a.sshTunnelId || 0) : { ...ETCD_DEFAULTS }),
    validate: (s) => {
      const ok = parseEtcdEndpoints(s.endpoints).length > 0;
      return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "etcd.error.endpointsRequired" };
    },
    build: async (s, ctx) => ({
      configJSON: buildEtcdConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "etcd",
      configJSON: buildEtcdConfig(s, resolveTestCredential(cred.value), s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(ETCD_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
});
