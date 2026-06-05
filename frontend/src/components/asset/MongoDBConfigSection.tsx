import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch, Tabs, TabsList, TabsTrigger, TabsContent } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildMongoDBConfig,
  parseMongoDBConfig,
  MONGODB_DEFAULTS,
  type MongoDBFormState,
} from "./MongoDBConfigSection.config";

export const MongoDBConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function MongoDBConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<MongoDBFormState>(() => {
    if (!editAsset) return { ...MONGODB_DEFAULTS };
    const parsed = parseMongoDBConfig(editAsset.Config);
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0)。
    return { ...parsed, sshTunnelId: editAsset.sshTunnelId || parsed.sshTunnelId };
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
        return { configJSON: buildMongoDBConfig(state, frag), sshTunnelId: state.sshTunnelId };
      },
      buildTestConfig: async () => ({
        assetType: "mongodb",
        // 测试无 asset 行 → 隧道必须塞进 config(includeSshAssetId=true,锁旧 handleTestMongoDBConnection)。
        configJSON: buildMongoDBConfig(state, resolveTestCredential(cred.value), true),
        password: cred.value.password,
      }),
    }),
    [state, cred.value]
  );

  return (
    <>
      {/* Connection & Auth (single visual block) */}
      <div className="grid gap-3 border rounded-lg p-3">
        {/* Connection Mode Toggle */}
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
                <Input value={state.host} onChange={(e) => patch({ host: e.target.value })} placeholder="example.com" />
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
      </div>

      {/* Default Database */}
      <div className="grid gap-2">
        <Label>{t("asset.mongoDefaultDatabase")}</Label>
        <Input
          value={state.database}
          onChange={(e) => patch({ database: e.target.value })}
          placeholder={t("asset.mongoDefaultDatabasePlaceholder")}
        />
      </div>

      {/* TLS */}
      <div className="flex items-center justify-between">
        <Label>{t("asset.tls")}</Label>
        <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
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
