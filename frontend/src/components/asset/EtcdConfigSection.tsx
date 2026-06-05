import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch, Textarea } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildEtcdConfig,
  parseEtcdConfig,
  parseEtcdEndpoints,
  ETCD_DEFAULTS,
  type EtcdFormState,
} from "./EtcdConfigSection.config";

export const EtcdConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function EtcdConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<EtcdFormState>(() => {
    if (!editAsset) return { ...ETCD_DEFAULTS };
    const parsed = parseEtcdConfig(editAsset.Config);
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0)。
    return { ...parsed, sshTunnelId: editAsset.sshTunnelId || parsed.sshTunnelId };
  });
  const patch = (p: Partial<EtcdFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // 端点为保存/测试共同必填;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = parseEtcdEndpoints(state.endpoints).length > 0;
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "etcd.error.endpointsRequired" });
  }, [state.endpoints, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        return { configJSON: buildEtcdConfig(state, frag), sshTunnelId: state.sshTunnelId };
      },
      buildTestConfig: async () => ({
        assetType: "etcd",
        configJSON: buildEtcdConfig(state, resolveTestCredential(cred.value)),
        password: cred.value.password,
      }),
    }),
    [state, cred.value]
  );

  return (
    <>
      {/* Connection & Auth(单视觉块) */}
      <div className="grid gap-3 border rounded-lg p-3">
        <div className="grid gap-2">
          <Label>{t("etcd.form.endpoints")}</Label>
          <Textarea
            value={state.endpoints}
            onChange={(e) => patch({ endpoints: e.target.value })}
            rows={3}
            className="font-mono text-sm"
            placeholder={"10.0.0.1:2379\n10.0.0.2:2379"}
          />
          <p className="text-xs text-muted-foreground">{t("etcd.form.endpointsHint")}</p>
        </div>

        <div className="grid gap-2">
          <Label>{t("asset.username")}</Label>
          <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
        </div>

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
            <Label>{t("etcd.form.tlsInsecure")}</Label>
            <Switch checked={state.tlsInsecure} onCheckedChange={(v) => patch({ tlsInsecure: v })} />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsServerName")}</Label>
            <Input
              value={state.tlsServerName}
              onChange={(e) => patch({ tlsServerName: e.target.value })}
              placeholder="etcd.example.com"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsCAFile")}</Label>
            <Input
              value={state.tlsCAFile}
              onChange={(e) => patch({ tlsCAFile: e.target.value })}
              placeholder="/path/to/ca.pem"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsCertFile")}</Label>
            <Input
              value={state.tlsCertFile}
              onChange={(e) => patch({ tlsCertFile: e.target.value })}
              placeholder="/path/to/client.crt"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsKeyFile")}</Label>
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
          <Label>{t("etcd.form.dialTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.dialTimeoutSeconds}
            onChange={(e) => patch({ dialTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("etcd.form.commandTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.commandTimeoutSeconds}
            onChange={(e) => patch({ commandTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
      </div>

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
