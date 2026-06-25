import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Switch } from "@opskat/ui";
import { Field, FieldLabel } from "@/components/asset/fields";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { resolveSaveProxyPassword } from "./proxyConfig";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { buildRedisConfig, parseRedisConfig, REDIS_DEFAULTS, type RedisFormState } from "./RedisConfigSection.config";
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";

export const RedisConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function RedisConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<RedisFormState>(() => {
    if (!editAsset) return { ...REDIS_DEFAULTS };
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0),
    // 并参与 connectionType 派生,故传入 parseRedisConfig。
    return parseRedisConfig(editAsset.Config, editAsset.sshTunnelId || 0);
  });
  const patch = (p: Partial<RedisFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // host 为保存/测试共同必填;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = !!state.host.trim();
    onValidityChange({
      canTest: ok,
      canSave: ok,
      saveDisabledReason: ok ? "" : "asset.formMissingHost",
    });
  }, [state.host, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        const proxyPassword = await resolveSaveProxyPassword(state, ctx.encryptPassword);
        return {
          configJSON: buildRedisConfig(state, frag, false, proxyPassword),
          sshTunnelId: state.connectionType === "jumphost" ? state.sshTunnelId : 0,
        };
      },
      buildTestConfig: async () => ({
        assetType: "redis",
        // 测试无 asset 行 → 隧道必须塞进 config(includeSshAssetId=true,锁旧 handleTestRedisConnection);
        // proxy 密码仅明文(无加密)。
        configJSON: buildRedisConfig(state, resolveTestCredential(cred.value), true, state.proxyPassword),
        password: cred.value.password,
      }),
    }),
    [state, cred.value]
  );

  const groups: ConfigGroup[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      render: () => (
        <div className="flex flex-col gap-4">
          {/* Host + Port (each labeled) */}
          <div className="flex items-end gap-3">
            <Field label={t("asset.host")} required className="flex-1">
              <Input
                data-testid="redis-host-input"
                value={state.host}
                onChange={(e) => patch({ host: e.target.value })}
                placeholder="example.com"
              />
            </Field>
            <Field label={t("asset.port")} className="w-[110px] shrink-0">
              <Input
                data-testid="redis-port-input"
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                value={state.port || ""}
                placeholder="6379"
                onChange={(e) => patch({ port: Number(e.target.value) })}
              />
            </Field>
          </div>

          {/* Username */}
          <Field label={t("asset.username")}>
            <Input
              value={state.username}
              onChange={(e) => patch({ username: e.target.value })}
              placeholder={t("asset.username") + " (" + t("asset.databasePlaceholder").split("（")[0] + ")"}
            />
          </Field>

          {/* Password */}
          <PasswordSourceField
            source={cred.value.passwordSource}
            onSourceChange={cred.setPasswordSource}
            password={cred.value.password}
            onPasswordChange={cred.setPassword}
            credentialId={cred.value.passwordCredentialId}
            onCredentialIdChange={cred.setPasswordCredentialId}
            managedPasswords={cred.managedPasswords}
            hasExistingPassword={!!cred.value.encryptedPassword}
            editAssetId={editAsset?.ID}
            onUsernameChange={(v) => patch({ username: v })}
          />

          {/* DB Number */}
          <Field label={t("asset.redisDatabase")}>
            <Input
              className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
              type="number"
              min={0}
              value={state.database}
              onChange={(e) => patch({ database: Math.max(0, Number(e.target.value) || 0) })}
            />
          </Field>
        </div>
      ),
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      render: () => <ConnectionMethodFields value={state} onChange={patch} />,
    },
    {
      key: "tls",
      label: "asset.tabTls",
      render: () => (
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between">
            <FieldLabel>{t("asset.tls")}</FieldLabel>
            <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
          </div>

          {state.tls && (
            <>
              <div className="flex items-center justify-between">
                <FieldLabel>{t("asset.redisTlsInsecure")}</FieldLabel>
                <Switch checked={state.tlsInsecure} onCheckedChange={(v) => patch({ tlsInsecure: v })} />
              </div>

              <Field label={t("asset.redisTlsServerName")}>
                <Input
                  value={state.tlsServerName}
                  onChange={(e) => patch({ tlsServerName: e.target.value })}
                  placeholder="redis.example.com"
                />
              </Field>

              <Field label={t("asset.redisTlsCAFile")}>
                <Input
                  value={state.tlsCAFile}
                  onChange={(e) => patch({ tlsCAFile: e.target.value })}
                  placeholder="/path/to/ca.pem"
                />
              </Field>

              <Field label={t("asset.redisTlsCertFile")}>
                <Input
                  value={state.tlsCertFile}
                  onChange={(e) => patch({ tlsCertFile: e.target.value })}
                  placeholder="/path/to/client.crt"
                />
              </Field>

              <Field label={t("asset.redisTlsKeyFile")}>
                <Input
                  value={state.tlsKeyFile}
                  onChange={(e) => patch({ tlsKeyFile: e.target.value })}
                  placeholder="/path/to/client.key"
                />
              </Field>
            </>
          )}
        </div>
      ),
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      render: () => (
        <div className="flex flex-col gap-4">
          <div className="flex items-end gap-3">
            <Field label={t("asset.redisCommandTimeout")} className="flex-1">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                min={0}
                value={state.commandTimeoutSeconds}
                onChange={(e) => patch({ commandTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
              />
            </Field>
            <Field label={t("asset.redisScanPageSize")} className="flex-1">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                min={0}
                value={state.scanPageSize}
                onChange={(e) => patch({ scanPageSize: Math.max(0, Number(e.target.value) || 0) })}
              />
            </Field>
          </div>
          <Field label={t("asset.redisKeySeparator")}>
            <Input
              value={state.keySeparator}
              onChange={(e) => patch({ keySeparator: e.target.value })}
              placeholder=":"
            />
          </Field>
        </div>
      ),
    },
  ];

  return <ConfigTabs groups={groups} />;
});
