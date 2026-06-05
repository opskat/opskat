import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { buildRedisConfig, parseRedisConfig, REDIS_DEFAULTS, type RedisFormState } from "./RedisConfigSection.config";

export const RedisConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function RedisConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<RedisFormState>(() => {
    if (!editAsset) return { ...REDIS_DEFAULTS };
    const parsed = parseRedisConfig(editAsset.Config);
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0)。
    return { ...parsed, sshTunnelId: editAsset.sshTunnelId || parsed.sshTunnelId };
  });
  const patch = (p: Partial<RedisFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // host 为保存/测试共同必填;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = !!state.host.trim();
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingHost" });
  }, [state.host, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        return { configJSON: buildRedisConfig(state, frag), sshTunnelId: state.sshTunnelId };
      },
      buildTestConfig: async () => ({
        assetType: "redis",
        // 测试无 asset 行 → 隧道必须塞进 config(includeSshAssetId=true,锁旧 handleTestRedisConnection)。
        configJSON: buildRedisConfig(state, resolveTestCredential(cred.value), true),
        password: cred.value.password,
      }),
    }),
    [state, cred.value]
  );

  return (
    <>
      {/* Connection & Auth (single visual block) */}
      <div className="grid gap-3 border rounded-lg p-3">
        {/* Host + Port (each labeled) */}
        <div className="grid grid-cols-[1fr_120px] gap-3">
          <div className="grid gap-2">
            <Label>{t("asset.host")}</Label>
            <Input value={state.host} onChange={(e) => patch({ host: e.target.value })} placeholder="example.com" />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.port")}</Label>
            <Input
              className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
              type="number"
              value={state.port || ""}
              placeholder="6379"
              onChange={(e) => patch({ port: Number(e.target.value) })}
            />
          </div>
        </div>

        {/* Username */}
        <div className="grid gap-2">
          <Label>{t("asset.username")}</Label>
          <Input
            value={state.username}
            onChange={(e) => patch({ username: e.target.value })}
            placeholder={t("asset.username") + " (" + t("asset.databasePlaceholder").split("（")[0] + ")"}
          />
        </div>

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
      </div>

      {/* TLS */}
      <div className="flex items-center justify-between">
        <Label>{t("asset.tls")}</Label>
        <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
      </div>

      {state.tls && (
        <>
          <div className="flex items-center justify-between">
            <Label>{t("asset.redisTlsInsecure")}</Label>
            <Switch checked={state.tlsInsecure} onCheckedChange={(v) => patch({ tlsInsecure: v })} />
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.redisTlsServerName")}</Label>
            <Input
              value={state.tlsServerName}
              onChange={(e) => patch({ tlsServerName: e.target.value })}
              placeholder="redis.example.com"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.redisTlsCAFile")}</Label>
            <Input
              value={state.tlsCAFile}
              onChange={(e) => patch({ tlsCAFile: e.target.value })}
              placeholder="/path/to/ca.pem"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.redisTlsCertFile")}</Label>
            <Input
              value={state.tlsCertFile}
              onChange={(e) => patch({ tlsCertFile: e.target.value })}
              placeholder="/path/to/client.crt"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("asset.redisTlsKeyFile")}</Label>
            <Input
              value={state.tlsKeyFile}
              onChange={(e) => patch({ tlsKeyFile: e.target.value })}
              placeholder="/path/to/client.key"
            />
          </div>
        </>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div className="grid gap-2">
          <Label>{t("asset.redisDatabase")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.database}
            onChange={(e) => patch({ database: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.redisCommandTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.commandTimeoutSeconds}
            onChange={(e) => patch({ commandTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="grid gap-2">
          <Label>{t("asset.redisScanPageSize")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.scanPageSize}
            onChange={(e) => patch({ scanPageSize: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.redisKeySeparator")}</Label>
          <Input value={state.keySeparator} onChange={(e) => patch({ keySeparator: e.target.value })} placeholder=":" />
        </div>
      </div>

      {/* SSH Tunnel */}
      <div className="grid gap-2">
        <Label>{t("asset.sshTunnel")}</Label>
        <AssetSelect
          value={state.sshTunnelId}
          onValueChange={(v) => patch({ sshTunnelId: v })}
          filterType="ssh"
          placeholder={t("asset.sshTunnelNone")}
        />
      </div>
    </>
  );
});
