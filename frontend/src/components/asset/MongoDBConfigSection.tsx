import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { resolveSaveProxyPassword } from "./proxyConfig";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildMongoDBConfig,
  parseMongoDBConfig,
  MONGODB_DEFAULTS,
  type MongoDBFormState,
} from "./MongoDBConfigSection.config";
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";

export const MongoDBConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function MongoDBConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<MongoDBFormState>(() => {
    if (!editAsset) return { ...MONGODB_DEFAULTS };
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0),
    // 并参与 connectionType 派生,故传入 parseMongoDBConfig。
    return parseMongoDBConfig(editAsset.Config, editAsset.sshTunnelId || 0);
  });
  const patch = (p: Partial<MongoDBFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // 保存/测试必填:mode 依赖校验;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = state.connectionMode === "uri" ? !!state.connectionURI.trim() : !!state.host.trim();
    const saveDisabledReason = ok
      ? ""
      : state.connectionMode === "uri"
        ? "asset.formMissingMongoUri"
        : "asset.formMissingHost";
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason });
  }, [state.connectionMode, state.connectionURI, state.host, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        const proxyPassword = await resolveSaveProxyPassword(state, ctx.encryptPassword);
        return {
          configJSON: buildMongoDBConfig(state, frag, false, proxyPassword),
          sshTunnelId: state.connectionType === "jumphost" ? state.sshTunnelId : 0,
        };
      },
      buildTestConfig: async () => ({
        assetType: "mongodb",
        // 测试无 asset 行 → 隧道必须塞进 config(includeSshAssetId=true,锁旧 handleTestMongoDBConnection);
        // proxy 密码仅明文(无加密)。
        configJSON: buildMongoDBConfig(state, resolveTestCredential(cred.value), true, state.proxyPassword),
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
          {/* Connection Mode Toggle (manual fields vs connection URI) */}
          <Segmented
            value={state.connectionMode}
            onChange={(v) => patch({ connectionMode: v })}
            aria-label={t("asset.mongoUri")}
            options={[
              { value: "manual", label: "Manual" },
              { value: "uri", label: "URI" },
            ]}
          />

          {state.connectionMode === "manual" && (
            /* Host + Port (each labeled) */
            <div className="flex items-end gap-3">
              <Field label={t("asset.host")} className="flex-1">
                <Input value={state.host} onChange={(e) => patch({ host: e.target.value })} placeholder="example.com" />
              </Field>
              <Field label={t("asset.port")} className="w-[110px] shrink-0">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  value={state.port || ""}
                  placeholder="27017"
                  onChange={(e) => patch({ port: Number(e.target.value) })}
                />
              </Field>
            </div>
          )}

          {state.connectionMode === "uri" && (
            /* Connection URI */
            <Field label={t("asset.mongoUri")}>
              <Input
                value={state.connectionURI}
                onChange={(e) => patch({ connectionURI: e.target.value })}
                placeholder={t("asset.mongoUriPlaceholder")}
              />
            </Field>
          )}

          {/* Username */}
          <Field label={t("asset.username")}>
            <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
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

          {/* Default Database */}
          <Field label={t("asset.mongoDefaultDatabase")}>
            <Input
              value={state.database}
              onChange={(e) => patch({ database: e.target.value })}
              placeholder={t("asset.mongoDefaultDatabasePlaceholder")}
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
          {/* TLS toggle (MongoDB: toggle only, no cert files) */}
          <div className="flex items-center justify-between">
            <Label>{t("asset.tls")}</Label>
            <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
          </div>
        </div>
      ),
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      render: () => (
        <div className="flex flex-col gap-4">
          {/* Replica Set */}
          <Field label={t("asset.mongoReplicaSet")}>
            <Input
              value={state.replicaSet}
              onChange={(e) => patch({ replicaSet: e.target.value })}
              placeholder={t("asset.mongoReplicaSetPlaceholder")}
            />
          </Field>

          {/* Auth Source */}
          <Field label={t("asset.mongoAuthSource")}>
            <Input
              value={state.authSource}
              onChange={(e) => patch({ authSource: e.target.value })}
              placeholder={t("asset.mongoAuthSourcePlaceholder")}
            />
          </Field>
        </div>
      ),
    },
  ];

  return <ConfigTabs groups={groups} />;
});
