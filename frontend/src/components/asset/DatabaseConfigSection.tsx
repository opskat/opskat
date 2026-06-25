import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch } from "@opskat/ui";
import { Field, FieldLabel, Segmented } from "@/components/asset/fields";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { AssetSelect } from "@/components/asset/AssetSelect";
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
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";

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
  const isRemoteSqlite = isSqlite && state.sqliteSource === "remote_ssh_vfs";

  // driver 切换:section 自有字段复位(纯函数)+ 壳 icon 副作用(onIconChange)。
  const handleDriverChange = (newDriver: string) => {
    setState((s) => applyDriverChange(s, newDriver));
    onIconChange?.(driverIcon(newDriver));
  };

  // 保存/测试必填:sqlite→path;非 sqlite→host;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const canSave = isSqlite ? !!state.path.trim() && (!isRemoteSqlite || state.sshTunnelId > 0) : !!state.host.trim();
    const saveDisabledReason = canSave
      ? ""
      : isRemoteSqlite && state.path.trim() && state.sshTunnelId <= 0
        ? "asset.formMissingSQLiteSSH"
        : isSqlite
          ? "asset.formMissingPath"
          : "asset.formMissingHost";
    onValidityChange({
      canTest: canSave,
      canSave,
      saveDisabledReason,
    });
  }, [isSqlite, isRemoteSqlite, state.path, state.host, state.sshTunnelId, onValidityChange]);

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

  const groups: ConfigGroup[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      render: () => (
        <div className="flex flex-col gap-4">
          {/* Database Driver */}
          <Field label={t("asset.driver")}>
            <Select value={state.driver} onValueChange={handleDriverChange}>
              <SelectTrigger data-testid="database-driver-select" className="w-full">
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
          </Field>

          {isSqlite ? (
            <>
              {/* SQLite path field */}
              <Field label={t("asset.sqliteSource")}>
                <Segmented
                  value={state.sqliteSource}
                  onChange={(v) => {
                    if (v === "remote_ssh_vfs") {
                      patch({ sqliteSource: "remote_ssh_vfs", connectionType: "jumphost" });
                    } else {
                      patch({ sqliteSource: "local", sshTunnelId: 0, connectionType: "direct" });
                    }
                  }}
                  aria-label={t("asset.sqliteSource")}
                  options={[
                    {
                      value: "local",
                      label: t("asset.sqliteSourceLocal"),
                      testid: "database-sqlite-source-option-local",
                    },
                    {
                      value: "remote_ssh_vfs",
                      label: t("asset.sqliteSourceRemoteSSH"),
                      testid: "database-sqlite-source-option-remote",
                    },
                  ]}
                />
              </Field>
              <Field label={t("asset.sqliteFilePath")}>
                <div className="flex gap-2">
                  <Input
                    data-testid="database-sqlite-path-input"
                    value={state.path}
                    onChange={(e) => patch({ path: e.target.value })}
                    placeholder={isRemoteSqlite ? "/var/lib/app/app.db" : t("asset.sqliteFilePathPlaceholder")}
                  />
                  {!isRemoteSqlite && (
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
                  )}
                </div>
              </Field>
              {isRemoteSqlite && (
                <Field label={t("asset.sqliteRemoteSSH")}>
                  <AssetSelect
                    value={state.sshTunnelId}
                    onValueChange={(v) => patch({ sshTunnelId: v, connectionType: "jumphost" })}
                    filterType="ssh"
                    placeholder={t("asset.jumpHostNone")}
                    testId="database-sqlite-ssh-select"
                  />
                </Field>
              )}
            </>
          ) : (
            <>
              {/* Host + Port (each labeled) */}
              <div className="flex items-end gap-3">
                <Field label={t("asset.host")} required className="flex-1">
                  <Input
                    data-testid="database-host-input"
                    value={state.host}
                    onChange={(e) => patch({ host: e.target.value })}
                    placeholder="example.com"
                  />
                </Field>
                <Field label={t("asset.port")} className="w-[110px] shrink-0">
                  <Input
                    data-testid="database-port-input"
                    className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                    type="number"
                    value={state.port || ""}
                    placeholder={state.driver === "postgresql" ? "5432" : state.driver === "mssql" ? "1433" : "3306"}
                    onChange={(e) => patch({ port: Number(e.target.value) })}
                  />
                </Field>
              </div>

              {/* Username */}
              <Field label={t("asset.username")}>
                <Input
                  data-testid="database-username-input"
                  value={state.username}
                  onChange={(e) => patch({ username: e.target.value })}
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

              {/* Database name */}
              <Field label={t("asset.database")}>
                <Input
                  data-testid="database-name-input"
                  value={state.database}
                  onChange={(e) => patch({ database: e.target.value })}
                  placeholder={t("asset.databasePlaceholder")}
                />
              </Field>
            </>
          )}
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
          {/* SSL Mode (PostgreSQL only) */}
          {state.driver === "postgresql" && (
            <Field label={t("asset.sslMode")}>
              <Select value={state.sslMode} onValueChange={(v) => patch({ sslMode: v })}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="disable">disable</SelectItem>
                  <SelectItem value="require">require</SelectItem>
                  <SelectItem value="verify-ca">verify-ca</SelectItem>
                  <SelectItem value="verify-full">verify-full</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}

          {/* TLS (MySQL + MSSQL) */}
          {(state.driver === "mysql" || state.driver === "mssql") && (
            <div className="flex items-center justify-between">
              <FieldLabel>TLS</FieldLabel>
              <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
            </div>
          )}
        </div>
      ),
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      render: () => (
        <div className="flex flex-col gap-4">
          {/* Params */}
          <Field label={t("asset.params")}>
            <Input
              value={state.params}
              onChange={(e) => patch({ params: e.target.value })}
              placeholder={t("asset.paramsPlaceholder")}
            />
          </Field>

          {/* Read Only */}
          <div className="flex items-center justify-between">
            <FieldLabel>{t("asset.readOnly")}</FieldLabel>
            <Switch checked={state.readOnly} onCheckedChange={(v) => patch({ readOnly: v })} />
          </div>
        </div>
      ),
    },
  ];

  return <ConfigTabs groups={groups} />;
});
