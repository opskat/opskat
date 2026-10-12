import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups } from "@/components/asset/configFields";
import {
  driverFormGroups,
  driverUsesField,
  findDatabaseDriver,
  useDatabaseDrivers,
  type DriverDescriptor,
} from "@/lib/dbDrivers";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import { proxyChainValidationKey, resolveSaveProxyChainSecrets, resolveSaveProxyPassword } from "./proxyConfig";
import {
  applyDriverChange,
  buildDatabaseConfig,
  parseDatabaseConfig,
  DATABASE_DEFAULTS,
  type DatabaseFormState,
} from "./DatabaseConfigSection.config";
import type { ConfigSectionProps } from "@/lib/assetTypes/formContract";

/** 驱动描述从后端取;取到之前不渲染表单,取失败显示该错误,不使用任何内置驱动列表。 */
export function DatabaseConfigSection(props: ConfigSectionProps) {
  const loaded = useDatabaseDrivers();
  if (loaded.status === "loading") return null;
  if (loaded.status === "error") {
    return (
      <p role="alert" className="text-sm text-destructive">
        {loaded.error}
      </p>
    );
  }
  return <DatabaseConfigForm {...props} drivers={loaded.drivers} />;
}

function DatabaseConfigForm({
  editAsset,
  onValidityChange,
  onIconChange,
  ref,
  drivers,
}: ConfigSectionProps & { drivers: DriverDescriptor[] }) {
  const { t } = useTranslation();
  const cred = useAssetCredential(editAsset);
  const { state, setState, patch } = useConfigSection<DatabaseFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) =>
      a ? parseDatabaseConfig(a.Config, a.sshTunnelId || 0) : applyDriverChange(DATABASE_DEFAULTS, drivers[0]),
    validate: (s) => {
      const d = findDatabaseDriver(drivers, s.driver);
      // 资产存的驱动不在注册表里:表单显示未知驱动错误,不放行保存。
      if (!d) return { canTest: false, canSave: false, saveDisabledReason: "" };
      const needsPath = driverUsesField(d, "path");
      const needsHost = driverUsesField(d, "host");
      const isRemote = driverUsesField(d, "sqlite_source") && s.sqliteSource === "remote_ssh_vfs";
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      const baseOk =
        (!needsPath || !!s.path.trim()) && (!isRemote || s.sshTunnelId > 0) && (!needsHost || !!s.host.trim());
      const canSave = baseOk && !proxyChainError;
      let saveDisabledReason = "";
      if (!canSave) {
        if (proxyChainError) saveDisabledReason = proxyChainError;
        else if (isRemote && s.path.trim() && s.sshTunnelId <= 0) {
          saveDisabledReason = "asset.formMissingSQLiteSSH";
        } else if (needsPath && !s.path.trim()) {
          saveDisabledReason = "asset.formMissingPath";
        } else {
          saveDisabledReason = "asset.formMissingHost";
        }
      }
      return { canTest: canSave, canSave, saveDisabledReason };
    },
    build: async (s, ctx) => ({
      configJSON: buildDatabaseConfig(
        s,
        driverOf(drivers, s.driver),
        await resolveSaveCredential(cred.value, ctx.encryptPassword),
        await resolveSaveProxyPassword(s, ctx.encryptPassword),
        await resolveSaveProxyChainSecrets(s.proxyChainLayers, ctx.encryptPassword)
      ),
      sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
    }),
    buildTest: async (s) => ({
      assetType: "database",
      configJSON: buildDatabaseConfig(
        s,
        driverOf(drivers, s.driver),
        resolveTestCredential(cred.value),
        s.proxyPassword,
        Object.fromEntries(
          s.proxyChainLayers.map((layer) => [layer.id, { password: layer.password, token: layer.token }])
        )
      ),
      password: cred.value.password,
    }),
    deps: [cred.value],
    validityDeps: [drivers],
  });

  const driver = findDatabaseDriver(drivers, state.driver);

  // 新建时壳的图标跟随默认选中的驱动;编辑态沿用资产自己的图标。
  useEffect(() => {
    if (!editAsset && driver) onIconChange?.(driver.icon);
    // 只在挂载时同步一次,之后图标由用户切换驱动驱动。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!driver) {
    return (
      <p role="alert" className="text-sm text-destructive">
        {t("asset.driverUnknown", { driver: state.driver })}
      </p>
    );
  }

  // driver 切换:section 自有字段复位(纯函数)+ 壳 icon 副作用(onIconChange)。
  const handleDriverChange = (id: string) => {
    const next = findDatabaseDriver(drivers, id);
    if (!next) return;
    setState((s) => applyDriverChange(s, next));
    onIconChange?.(next.icon);
  };

  const driverSelect = {
    kind: "custom" as const,
    render: () => (
      <Field label={t("asset.driver")}>
        <Select value={state.driver} onValueChange={handleDriverChange}>
          <SelectTrigger data-testid="database-driver-select" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {drivers.map((d) => (
              <SelectItem key={d.id} value={d.id} data-testid={`database-driver-option-${d.id}`}>
                {d.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
    ),
  };

  const groups = driverFormGroups(driver, [driverSelect]);

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
}

/** 表单状态里的驱动一定来自注册表(新建取自列表、编辑在不认识时已停在错误提示),保存 / 测试时据此取描述。 */
function driverOf(drivers: DriverDescriptor[], id: string): DriverDescriptor {
  const d = findDatabaseDriver(drivers, id);
  if (!d) throw new Error(`database driver ${id} is not registered`);
  return d;
}
