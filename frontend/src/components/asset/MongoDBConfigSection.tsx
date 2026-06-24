import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch, Tabs, TabsList, TabsTrigger, TabsContent } from "@opskat/ui";
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
import { ConfigTabs, type ConfigGroup, type ConfigTabsHandle } from "@/components/asset/ConfigTabs";

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
  const tabsRef = useRef<ConfigTabsHandle>(null);

  // 保存/测试必填:mode 依赖校验;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = state.connectionMode === "uri" ? !!state.connectionURI.trim() : !!state.host.trim();
    const saveDisabledReason = ok
      ? ""
      : state.connectionMode === "uri"
        ? "asset.formMissingMongoUri"
        : "asset.formMissingHost";
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason, invalidGroupKey: ok ? undefined : "connection" });
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
      focusGroup: (key) => tabsRef.current?.setActive(key),
    }),
    [state, cred.value]
  );

  const groups: ConfigGroup[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      invalid: !(state.connectionMode === "uri" ? state.connectionURI.trim() : state.host.trim()),
      render: () => (
        <div className="grid gap-3">
          {/* Connection Mode Toggle (inner manual/URI sub-switch — kept verbatim) */}
          <Tabs value={state.connectionMode} onValueChange={(v) => patch({ connectionMode: v as "manual" | "uri" })}>
            <TabsList className="grid w-full grid-cols-2">
              <TabsTrigger value="manual">Manual</TabsTrigger>
              <TabsTrigger value="uri">URI</TabsTrigger>
            </TabsList>

            <TabsContent value="manual" className="space-y-3 mt-3">
              {/* Host + Port (each labeled) */}
              <div className="grid grid-cols-[1fr_120px] gap-3">
                <div className="grid gap-2">
                  <Label>{t("asset.host")}</Label>
                  <Input
                    value={state.host}
                    onChange={(e) => patch({ host: e.target.value })}
                    placeholder="example.com"
                  />
                </div>
                <div className="grid gap-2">
                  <Label>{t("asset.port")}</Label>
                  <Input
                    className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                    type="number"
                    value={state.port || ""}
                    placeholder="27017"
                    onChange={(e) => patch({ port: Number(e.target.value) })}
                  />
                </div>
              </div>
            </TabsContent>

            <TabsContent value="uri" className="space-y-3 mt-3">
              {/* Connection URI */}
              <div className="grid gap-2">
                <Label>{t("asset.mongoUri")}</Label>
                <Input
                  value={state.connectionURI}
                  onChange={(e) => patch({ connectionURI: e.target.value })}
                  placeholder={t("asset.mongoUriPlaceholder")}
                />
              </div>
            </TabsContent>
          </Tabs>

          {/* Username */}
          <div className="grid gap-2">
            <Label>{t("asset.username")}</Label>
            <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
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

          {/* Default Database */}
          <div className="grid gap-2">
            <Label>{t("asset.mongoDefaultDatabase")}</Label>
            <Input
              value={state.database}
              onChange={(e) => patch({ database: e.target.value })}
              placeholder={t("asset.mongoDefaultDatabasePlaceholder")}
            />
          </div>
        </div>
      ),
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      optional: true,
      render: () => <ConnectionMethodFields value={state} onChange={patch} />,
    },
    {
      key: "tls",
      label: "asset.tabTls",
      optional: true,
      render: () => (
        <div className="grid gap-3">
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
      optional: true,
      render: () => (
        <div className="grid gap-3">
          {/* Replica Set */}
          <div className="grid gap-2">
            <Label>{t("asset.mongoReplicaSet")}</Label>
            <Input
              value={state.replicaSet}
              onChange={(e) => patch({ replicaSet: e.target.value })}
              placeholder={t("asset.mongoReplicaSetPlaceholder")}
            />
          </div>

          {/* Auth Source */}
          <div className="grid gap-2">
            <Label>{t("asset.mongoAuthSource")}</Label>
            <Input
              value={state.authSource}
              onChange={(e) => patch({ authSource: e.target.value })}
              placeholder={t("asset.mongoAuthSourcePlaceholder")}
            />
          </div>
        </div>
      ),
    },
  ];

  return <ConfigTabs ref={tabsRef} groups={groups} />;
});
