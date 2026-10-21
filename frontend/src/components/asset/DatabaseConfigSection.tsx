import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups } from "@/components/asset/configFields";
import {
  buildDriverOptions,
  driverFormGroups,
  driverUsesField,
  invalidDriverField,
  missingRequiredDriverField,
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
      // 资产存的驱动不在注册表里:表单显示未知驱动错误,配置原样保留,仍可保存通用字段;
      // 测试连接交给后端,由它回答「驱动不可用」。新建的资产没有存下的配置可保留(所选驱动
      // 在填写途中被移除):不能保存也不能测试,改选别的驱动才行。
      if (!d) return editAsset ? { canTest: true, canSave: true } : { canTest: false, canSave: false };
      const needsPath = driverUsesField(d, "path");
      const needsHost = driverUsesField(d, "host");
      const isRemote = driverUsesField(d, "sqlite_source") && s.sqliteSource === "remote_ssh_vfs";
      const proxyChainError = proxyChainValidationKey(s.proxyChainLayers);
      const baseOk =
        (!needsPath || !!s.path.trim()) && (!isRemote || s.sshTunnelId > 0) && (!needsHost || !!s.host.trim());
      const driverFieldInvalid = invalidDriverField(d, s);
      const driverFieldsOk = !driverFieldInvalid && !missingRequiredDriverField(d, s);
      const canSave = baseOk && driverFieldsOk && !proxyChainError;
      let saveDisabledReason = "";
      if (!canSave) {
        if (proxyChainError) saveDisabledReason = proxyChainError;
        else if (baseOk && driverFieldInvalid) saveDisabledReason = "asset.formInvalidDriverField";
        else if (baseOk) saveDisabledReason = "asset.formMissingDriverField";
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
    build: async (s, ctx) => {
      const d = findDatabaseDriver(drivers, s.driver);
      if (!d) return storedConfigOf(editAsset);
      return {
        configJSON: buildDatabaseConfig(
          s,
          d,
          await resolveSaveCredential(cred.value, ctx.encryptPassword),
          await resolveSaveProxyPassword(s, ctx.encryptPassword),
          await resolveSaveProxyChainSecrets(s.proxyChainLayers, ctx.encryptPassword),
          await buildDriverOptions(d, s, ctx.encryptPassword)
        ),
        sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
      };
    },
    buildTest: async (s, ctx) => {
      const d = findDatabaseDriver(drivers, s.driver);
      if (!d)
        return {
          assetType: "database",
          configJSON: storedConfigOf(editAsset).configJSON,
          password: cred.value.password,
        };
      return {
        assetType: "database",
        configJSON: buildDatabaseConfig(
          s,
          d,
          resolveTestCredential(cred.value),
          s.proxyPassword,
          Object.fromEntries(
            s.proxyChainLayers.map((layer) => [layer.id, { password: layer.password, token: layer.token }])
          ),
          // 测试连接与已存资产一样由后端解密口令类专属字段,所以这里同样给密文。
          await buildDriverOptions(d, s, ctx.encryptPassword)
        ),
        password: cred.value.password,
      };
    },
    // build 按当前的驱动描述序列化:描述更新(扩展升级、换语言)后保存用的是新的那份。
    deps: [cred.value, drivers],
    validityDeps: [drivers],
  });

  const driver = findDatabaseDriver(drivers, state.driver);

  // 新建时壳的图标跟随默认选中的驱动;编辑态沿用资产自己的图标。
  useEffect(() => {
    if (!editAsset && driver) onIconChange?.(driver.icon);
    // 只在挂载时同步一次,之后图标由用户切换驱动驱动。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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

  if (!driver) {
    const unknown = (
      <p role="alert" className="text-sm text-destructive">
        {t("asset.driverUnknown", { driver: state.driver })}
      </p>
    );
    if (editAsset) return unknown;
    // 新建时所选驱动在填写途中被移除:留着驱动下拉,改选别的驱动即可继续。
    return (
      <div className="flex flex-col gap-3">
        {unknown}
        {driverSelect.render()}
      </div>
    );
  }

  const groups = driverFormGroups(driver, [driverSelect]);

  return <ConfigTabs groups={buildConfigGroups(groups, { state, patch, ctx: { cred, editAsset } })} />;
}

/** 驱动未注册的资产没有可用的描述来重新序列化:保存与测试都沿用库里存的配置原文。 */
function storedConfigOf(editAsset: ConfigSectionProps["editAsset"]) {
  if (!editAsset) throw new Error("an unregistered database driver can only come from an existing asset");
  return { configJSON: editAsset.Config, sshTunnelId: editAsset.sshTunnelId || 0 };
}
