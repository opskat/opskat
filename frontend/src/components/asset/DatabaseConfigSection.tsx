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
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { resolveSaveProxyPassword } from "./proxyConfig";
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
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0),
    // 并参与 connectionType 派生,故传入 parseDatabaseConfig。
    return parseDatabaseConfig(editAsset.Config, editAsset.sshTunnelId || 0);
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
        const proxyPassword = await resolveSaveProxyPassword(state, ctx.encryptPassword);
        return {
          configJSON: buildDatabaseConfig(state, frag, proxyPassword),
          sshTunnelId: state.connectionType === "jumphost" ? state.sshTunnelId : 0,
        };
      },
      buildTestConfig: async () => ({
        assetType: "database",
        // 测试:proxy 密码仅明文(无加密)
        configJSON: buildDatabaseConfig(state, resolveTestCredential(cred.value), state.proxyPassword),
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
          <SelectTrigger data-testid="database-driver-select">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="mysql" data-testid="database-driver-option-mysql">
              {t("asset.driverMySQL")}
            </SelectItem>
            <SelectItem value="postgresql" data-testid="database-driver-option-postgresql">
              {t("asset.driverPostgreSQL")}
            </SelectItem>
            <SelectItem value="mssql" data-testid="database-driver-option-mssql">
              {t("asset.driverMSSQL")}
            </SelectItem>
            <SelectItem value="sqlite" data-testid="database-driver-option-sqlite">
              {t("asset.driverSQLite")}
            </SelectItem>
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
                <Input
                  data-testid="database-host-input"
                  value={state.host}
                  onChange={(e) => patch({ host: e.target.value })}
                  placeholder="example.com"
                />
              </div>
              <div className="grid gap-2">
                <Label>{t("asset.port")}</Label>
                <Input
                  data-testid="database-port-input"
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
              <Input
                data-testid="database-username-input"
                value={state.username}
                onChange={(e) => patch({ username: e.target.value })}
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

          {/* Database name */}
          <div className="grid gap-2">
            <Label>{t("asset.database")}</Label>
            <Input
              data-testid="database-name-input"
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

          {/* Connection method: direct / SSH tunnel / SOCKS5 proxy */}
          <ConnectionMethodFields value={state} onChange={patch} />
        </>
      )}
    </>
  );
});
