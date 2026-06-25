import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch, Textarea } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
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
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";

export const EtcdConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function EtcdConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<EtcdFormState>(() => {
    if (!editAsset) return { ...ETCD_DEFAULTS };
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0),
    // 并参与 connectionType 派生,故传入 parseEtcdConfig。
    return parseEtcdConfig(editAsset.Config, editAsset.sshTunnelId || 0);
  });
  const patch = (p: Partial<EtcdFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // 端点为保存/测试共同必填;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = parseEtcdEndpoints(state.endpoints).length > 0;
    onValidityChange({
      canTest: ok,
      canSave: ok,
      saveDisabledReason: ok ? "" : "etcd.error.endpointsRequired",
    });
  }, [state.endpoints, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        const proxyPassword = await resolveSaveProxyPassword(state, ctx.encryptPassword);
        return {
          configJSON: buildEtcdConfig(state, frag, proxyPassword),
          sshTunnelId: state.connectionType === "jumphost" ? state.sshTunnelId : 0,
        };
      },
      buildTestConfig: async () => ({
        assetType: "etcd",
        // 测试:proxy 密码仅明文(无加密)
        configJSON: buildEtcdConfig(state, resolveTestCredential(cred.value), state.proxyPassword),
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
          <Field label={t("etcd.form.endpoints")} required>
            <Textarea
              value={state.endpoints}
              onChange={(e) => patch({ endpoints: e.target.value })}
              rows={3}
              className="font-mono text-sm"
              placeholder={"10.0.0.1:2379\n10.0.0.2:2379"}
            />
            <p className="text-xs text-muted-foreground">{t("etcd.form.endpointsHint")}</p>
          </Field>

          <Field label={t("asset.username")}>
            <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
          </Field>

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

          <div className="flex items-end gap-3">
            <Field label={t("etcd.form.dialTimeout")} className="flex-1">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                min={0}
                value={state.dialTimeoutSeconds}
                onChange={(e) => patch({ dialTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
              />
            </Field>
            <Field label={t("etcd.form.commandTimeout")} className="flex-1">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                min={0}
                value={state.commandTimeoutSeconds}
                onChange={(e) => patch({ commandTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
              />
            </Field>
          </div>
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
            <Label>{t("asset.tls")}</Label>
            <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
          </div>

          {state.tls && (
            <>
              <div className="flex items-center justify-between">
                <Label>{t("etcd.form.tlsInsecure")}</Label>
                <Switch checked={state.tlsInsecure} onCheckedChange={(v) => patch({ tlsInsecure: v })} />
              </div>

              <Field label={t("etcd.form.tlsServerName")}>
                <Input
                  value={state.tlsServerName}
                  onChange={(e) => patch({ tlsServerName: e.target.value })}
                  placeholder="etcd.example.com"
                />
              </Field>

              <Field label={t("etcd.form.tlsCAFile")}>
                <Input
                  value={state.tlsCAFile}
                  onChange={(e) => patch({ tlsCAFile: e.target.value })}
                  placeholder="/path/to/ca.pem"
                />
              </Field>

              <Field label={t("etcd.form.tlsCertFile")}>
                <Input
                  value={state.tlsCertFile}
                  onChange={(e) => patch({ tlsCertFile: e.target.value })}
                  placeholder="/path/to/client.crt"
                />
              </Field>

              <Field label={t("etcd.form.tlsKeyFile")}>
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
  ];

  return <ConfigTabs groups={groups} />;
});
