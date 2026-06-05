import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { SelectSQLiteFile } from "../../../wailsjs/go/system/System";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  applyDriverChange,
  buildDatabaseConfig,
  driverIcon,
  parseDatabaseConfig,
  DATABASE_DEFAULTS,
  type DatabaseFormState,
} from "./DatabaseConfigSection.config";

export const DatabaseConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function DatabaseConfigSection(
  { editAsset, onValidityChange, onIconChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<DatabaseFormState>(() => {
    if (!editAsset) return { ...DATABASE_DEFAULTS };
    const parsed = parseDatabaseConfig(editAsset.Config);
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0)。
    return { ...parsed, sshTunnelId: editAsset.sshTunnelId || parsed.sshTunnelId };
  });
  const patch = (p: Partial<DatabaseFormState>) => setState((s) => ({ ...s, ...p }));
  // 凭据子状态:sqlite 无凭据,但 hook 始终持有;build 在 sqlite 分支忽略 cred。
  const cred = useAssetCredential(editAsset);

  const isSqlite = state.driver === "sqlite";

  // driver 切换:section 自有字段复位(纯函数)+ 壳 icon 副作用(onIconChange)。
  const handleDriverChange = (newDriver: string) => {
    setState((s) => applyDriverChange(s, newDriver));
    onIconChange?.(driverIcon(newDriver));
  };

  // 保存/测试必填:sqlite→path;非 sqlite→host;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = isSqlite ? !!state.path.trim() : !!state.host.trim();
    const saveDisabledReason = ok ? "" : isSqlite ? "asset.formMissingPath" : "asset.formMissingHost";
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason });
  }, [isSqlite, state.path, state.host, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        return { configJSON: buildDatabaseConfig(state, frag), sshTunnelId: state.sshTunnelId };
      },
      buildTestConfig: async () => ({
        assetType: "database",
        configJSON: buildDatabaseConfig(state, resolveTestCredential(cred.value)),
        password: cred.value.password,
      }),
    }),
    [state, cred.value]
  );

  return (
    <>
      {/* Database Driver (before host) */}
      <div className="grid gap-2">
        <Label>{t("asset.driver")}</Label>
        <Select value={state.driver} onValueChange={handleDriverChange}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="mysql">{t("asset.driverMySQL")}</SelectItem>
            <SelectItem value="postgresql">{t("asset.driverPostgreSQL")}</SelectItem>
            <SelectItem value="mssql">{t("asset.driverMSSQL")}</SelectItem>
            <SelectItem value="sqlite">{t("asset.driverSQLite")}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {isSqlite ? (
        <>
          {/* SQLite path field */}
          <div className="grid gap-3 border rounded-lg p-3">
            <div className="grid gap-2">
              <Label>{t("asset.sqliteFilePath")}</Label>
              <div className="flex gap-2">
                <Input
                  value={state.path}
                  onChange={(e) => patch({ path: e.target.value })}
                  placeholder={t("asset.sqliteFilePathPlaceholder")}
                />
                <Button
                  type="button"
                  variant="secondary"
                  onClick={async () => {
                    const selected = await SelectSQLiteFile();
                    if (selected) patch({ path: selected });
                  }}
                >
                  {t("asset.sqliteFilePathBrowse")}
                </Button>
              </div>
            </div>
          </div>

          {/* Params */}
          <div className="grid gap-2">
            <Label>{t("asset.params")}</Label>
            <Input
              value={state.params}
              onChange={(e) => patch({ params: e.target.value })}
              placeholder={t("asset.paramsPlaceholder")}
            />
          </div>

          {/* Read Only */}
          <div className="flex items-center justify-between">
            <Label>{t("asset.readOnly")}</Label>
            <Switch checked={state.readOnly} onCheckedChange={(v) => patch({ readOnly: v })} />
          </div>
        </>
      ) : (
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
                  placeholder={state.driver === "postgresql" ? "5432" : state.driver === "mssql" ? "1433" : "3306"}
                  onChange={(e) => patch({ port: Number(e.target.value) })}
                />
              </div>
            </div>

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

          {/* Database name */}
          <div className="grid gap-2">
            <Label>{t("asset.database")}</Label>
            <Input
              value={state.database}
              onChange={(e) => patch({ database: e.target.value })}
              placeholder={t("asset.databasePlaceholder")}
            />
          </div>

          {/* SSL Mode (PostgreSQL only) */}
          {state.driver === "postgresql" && (
            <div className="grid gap-2">
              <Label>{t("asset.sslMode")}</Label>
              <Select value={state.sslMode} onValueChange={(v) => patch({ sslMode: v })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="disable">disable</SelectItem>
                  <SelectItem value="require">require</SelectItem>
                  <SelectItem value="verify-ca">verify-ca</SelectItem>
                  <SelectItem value="verify-full">verify-full</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}

          {/* TLS (MySQL + MSSQL) */}
          {(state.driver === "mysql" || state.driver === "mssql") && (
            <div className="flex items-center justify-between">
              <Label>TLS</Label>
              <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
            </div>
          )}

          {/* Params */}
          <div className="grid gap-2">
            <Label>{t("asset.params")}</Label>
            <Input
              value={state.params}
              onChange={(e) => patch({ params: e.target.value })}
              placeholder={t("asset.paramsPlaceholder")}
            />
          </div>

          {/* Read Only */}
          <div className="flex items-center justify-between">
            <Label>{t("asset.readOnly")}</Label>
            <Switch checked={state.readOnly} onCheckedChange={(v) => patch({ readOnly: v })} />
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
      )}
    </>
  );
});
