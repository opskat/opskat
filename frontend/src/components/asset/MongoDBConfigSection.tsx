import { forwardRef } from "react";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import {
  buildMongoDBConfig,
  parseMongoDBConfig,
  MONGODB_DEFAULTS,
  type MongoDBFormState,
} from "./MongoDBConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

const MONGODB_GROUPS: ConfigGroupSchema<MongoDBFormState>[] = [
  {
    key: "connection",
    label: "asset.tabConnection",
    fields: [
      {
        kind: "segmented",
        key: "connectionMode",
        ariaLabel: "asset.mongoUri",
        options: [
          { value: "manual", label: "Manual" },
          { value: "uri", label: "URI" },
        ],
      },
      {
        kind: "row",
        visibleWhen: (s) => s.connectionMode === "manual",
        fields: [
          { kind: "text", key: "host", label: "asset.host", placeholder: "example.com", width: "flex-1" },
          {
            kind: "number",
            key: "port",
            label: "asset.port",
            placeholder: "27017",
            width: "w-[110px] shrink-0",
            blankWhenZero: true,
          },
        ],
      },
      {
        kind: "text",
        key: "connectionURI",
        label: "asset.mongoUri",
        placeholder: "asset.mongoUriPlaceholder",
        visibleWhen: (s) => s.connectionMode === "uri",
      },
      { kind: "text", key: "username", label: "asset.username" },
      { kind: "password" },
      {
        kind: "text",
        key: "database",
        label: "asset.mongoDefaultDatabase",
        placeholder: "asset.mongoDefaultDatabasePlaceholder",
      },
    ],
  },
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  { key: "tls", label: "asset.tabTls", fields: [{ kind: "switch", key: "tls", label: "asset.tls" }] },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    fields: [
      {
        kind: "text",
        key: "replicaSet",
        label: "asset.mongoReplicaSet",
        placeholder: "asset.mongoReplicaSetPlaceholder",
      },
      {
        kind: "text",
        key: "authSource",
        label: "asset.mongoAuthSource",
        placeholder: "asset.mongoAuthSourcePlaceholder",
      },
    ],
  },
];

export const MongoDBConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function MongoDBConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const cred = useAssetCredential(editAsset);
  const { state, patch } = useConfigSection<MongoDBFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseMongoDBConfig(a.Config, a.sshTunnelId || 0) : { ...MONGODB_DEFAULTS }),
    validate: (s) => {
      const ok = s.connectionMode === "uri" ? !!s.connectionURI.trim() : !!s.host.trim();
      const saveDisabledReason = ok
        ? ""
        : s.connectionMode === "uri"
          ? "asset.formMissingMongoUri"
          : "asset.formMissingHost";
      return { canTest: ok, canSave: ok, saveDisabledReason };
    },
    build: async (s, ctx) => ({
      configJSON: buildMongoDBConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        false,
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "mongodb",
      configJSON: buildMongoDBConfig(s, resolveTestCredential(cred.value), true, s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(MONGODB_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
});
