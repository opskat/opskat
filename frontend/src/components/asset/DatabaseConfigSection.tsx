import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { Button, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { SelectSQLiteFile } from "../../../wailsjs/go/system/System";
import {
  applyDriverChange,
  buildDatabaseConfig,
  driverIcon,
  parseDatabaseConfig,
  DATABASE_DEFAULTS,
  type DatabaseFormState,
} from "./DatabaseConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const DatabaseConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function DatabaseConfigSection(
  { editAsset, onValidityChange, onIconChange },
  ref
) {
  const { t } = useTranslation();
  const cred = useAssetCredential(editAsset);
  const { state, setState, patch } = useConfigSection<DatabaseFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseDatabaseConfig(a.Config, a.sshTunnelId || 0) : { ...DATABASE_DEFAULTS }),
    validate: (s) => {
      const isSqlite = s.driver === "sqlite";
      const isRemoteSqlite = isSqlite && s.sqliteSource === "remote_ssh_vfs";
      const canSave = isSqlite ? !!s.path.trim() && (!isRemoteSqlite || s.sshTunnelId > 0) : !!s.host.trim();
      const saveDisabledReason = canSave
        ? ""
        : isRemoteSqlite && s.path.trim() && s.sshTunnelId <= 0
          ? "asset.formMissingSQLiteSSH"
          : isSqlite
            ? "asset.formMissingPath"
            : "asset.formMissingHost";
      return { canTest: canSave, canSave, saveDisabledReason };
    },
    build: async (s, ctx) => ({
      configJSON: buildDatabaseConfig(
        s,
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        await resolveSaveProxyPassword(s, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "database",
      configJSON: buildDatabaseConfig(s, resolveTestCredential(cred.value), s.proxyPassword),
      password: cred.value.password,
    }),
    deps: [cred.value],
  });

  // driver 切换:section 自有字段复位(纯函数)+ 壳 icon 副作用(onIconChange)。
  const handleDriverChange = (newDriver: string) => {
    setState((s) => applyDriverChange(s, newDriver));
    onIconChange?.(driverIcon(newDriver));
  };

  const groups: ConfigGroupSchema<DatabaseFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "custom",
          render: () => (
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
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.driver === "sqlite",
          render: () => (
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
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.driver === "sqlite",
          render: () => {
            const isRemoteSqlite = state.sqliteSource === "remote_ssh_vfs";
            return (
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
            );
          },
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.driver === "sqlite" && s.sqliteSource === "remote_ssh_vfs",
          render: () => (
            <Field label={t("asset.sqliteRemoteSSH")}>
              <AssetSelect
                value={state.sshTunnelId}
                onValueChange={(v) => patch({ sshTunnelId: v, connectionType: "jumphost" })}
                filterType="ssh"
                placeholder={t("asset.jumpHostNone")}
                testId="database-sqlite-ssh-select"
              />
            </Field>
          ),
        },
        {
          kind: "row",
          visibleWhen: (s) => s.driver !== "sqlite",
          fields: [
            {
              kind: "text",
              key: "host",
              label: "asset.host",
              required: true,
              placeholder: "example.com",
              width: "flex-1",
              testid: "database-host-input",
            },
            {
              kind: "number",
              key: "port",
              label: "asset.port",
              width: "w-[110px] shrink-0",
              blankWhenZero: true,
              testid: "database-port-input",
            },
          ],
        },
        {
          kind: "text",
          key: "username",
          label: "asset.username",
          testid: "database-username-input",
          visibleWhen: (s) => s.driver !== "sqlite",
        },
        { kind: "password", visibleWhen: (s) => s.driver !== "sqlite" },
        {
          kind: "text",
          key: "database",
          label: "asset.database",
          placeholder: "asset.databasePlaceholder",
          testid: "database-name-input",
          visibleWhen: (s) => s.driver !== "sqlite",
        },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
    {
      key: "tls",
      label: "asset.tabTls",
      fields: [
        {
          kind: "select",
          key: "sslMode",
          label: "asset.sslMode",
          visibleWhen: (s) => s.driver === "postgresql",
          options: [
            { value: "disable", label: "disable" },
            { value: "require", label: "require" },
            { value: "verify-ca", label: "verify-ca" },
            { value: "verify-full", label: "verify-full" },
          ],
        },
        { kind: "switch", key: "tls", label: "TLS", visibleWhen: (s) => s.driver === "mysql" || s.driver === "mssql" },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        { kind: "text", key: "params", label: "asset.params", placeholder: "asset.paramsPlaceholder" },
        { kind: "switch", key: "readOnly", label: "asset.readOnly" },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
});
