import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey, resolveSaveProxyChainSecrets, resolveSaveProxyPassword } from "./proxyConfig";
import { buildRedisConfig, parseRedisConfig, REDIS_DEFAULTS, type RedisFormState } from "./RedisConfigSection.config";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";

const REDIS_GROUPS: ConfigGroupSchema<RedisFormState>[] = [
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
            placeholder: "example.com",
            width: "flex-1",
            testid: "redis-host-input",
          },
          {
            kind: "number",
            key: "port",
            label: "asset.port",
            placeholder: "6379",
            width: "w-[110px] shrink-0",
            blankWhenZero: true,
            testid: "redis-port-input",
          },
        ],
      },
      { kind: "text", key: "username", label: "asset.username" },
      { kind: "password" },
      { kind: "number", key: "database", label: "asset.redisDatabase", min: 0 },
    ],
  },
  { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  {
    key: "tls",
    label: "asset.tabTls",
    fields: [
      { kind: "switch", key: "tls", label: "asset.tls" },
      { kind: "switch", key: "tlsInsecure", label: "asset.redisTlsInsecure", visibleWhen: (s) => s.tls },
      {
        kind: "text",
        key: "tlsServerName",
        label: "asset.redisTlsServerName",
        placeholder: "redis.example.com",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsCAFile",
        label: "asset.redisTlsCAFile",
        placeholder: "/path/to/ca.pem",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsCertFile",
        label: "asset.redisTlsCertFile",
        placeholder: "/path/to/client.crt",
        visibleWhen: (s) => s.tls,
      },
      {
        kind: "text",
        key: "tlsKeyFile",
        label: "asset.redisTlsKeyFile",
        placeholder: "/path/to/client.key",
        visibleWhen: (s) => s.tls,
      },
    ],
  },
  {
    key: "advanced",
    label: "asset.tabAdvanced",
    fields: [
      {
        kind: "row",
        fields: [
          { kind: "number", key: "commandTimeoutSeconds", label: "asset.redisCommandTimeout", min: 0, width: "flex-1" },
          { kind: "number", key: "scanPageSize", label: "asset.redisScanPageSize", min: 0, width: "flex-1" },
        ],
      },
      { kind: "text", key: "keySeparator", label: "asset.redisKeySeparator", placeholder: ":" },
    ],
  },
];

export function RedisConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
  const cred = useAssetCredential(editAsset);
  const { state, patch } = useConfigSection<RedisFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseRedisConfig(a.Config, a.sshTunnelId || 0) : { ...REDIS_DEFAULTS }),
    validate: (s) => {
      const ok = !!s.host.trim();
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      const canUse = ok && !proxyChainError;
      return { canTest: canUse, canSave: canUse, saveDisabledReason: ok ? proxyChainError : "asset.formMissingHost" };
    },
    build: async (s, ctx) => ({
      configJSON: buildRedisConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        false,
        await resolveSaveProxyPassword(s, ctx.encryptPassword),
        await resolveSaveProxyChainSecrets(s.proxyChainLayers, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "redis",
      configJSON: buildRedisConfig(
        s,
        resolveTestCredential(cred.value),
        true,
        s.proxyPassword,
        Object.fromEntries(
          s.proxyChainLayers.map((layer) => [layer.id, { password: layer.password, token: layer.token }])
        )
      ),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  const groups = buildConfigGroups(REDIS_GROUPS, { state, patch, ctx: { cred, editAsset } });
  return <ConfigTabs groups={groups} />;
}
