import { useTranslation } from "react-i18next";
import { driverFieldsOf, findDatabaseDriver, useDatabaseDrivers, type DriverDescriptor } from "@/lib/dbDrivers";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import type { ProxyConfigJSON, ProxyChainJSON } from "../proxyConfig";
import {
  DetailGrid,
  DetailSection,
  InfoItem,
  ProxyChainDetailSection,
  ProxyDetailSection,
  TunnelInfo,
} from "./InfoItem";
import { DISABLED_VALUE, ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";

interface DatabaseConfig {
  driver: string;
  host: string;
  port: number;
  username: string;
  password?: string;
  database?: string;
  ssl_mode?: string;
  tls?: boolean;
  params?: string;
  read_only?: boolean;
  ssh_asset_id?: number;
  proxy?: ProxyConfigJSON | null;
  proxy_chain?: ProxyChainJSON | null;
  driver_options?: Record<string, unknown>;
}

export function DatabaseDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();
  const loaded = useDatabaseDrivers();

  const cfg = parseDetailConfig<DatabaseConfig>(asset.Config);
  if (!cfg) return null;
  const tunnelName = sshTunnelName(asset.sshTunnelId || cfg.ssh_asset_id);
  const descriptor = loaded.status === "ready" ? findDatabaseDriver(loaded.drivers, cfg.driver) : undefined;

  return (
    <>
      <DetailSection title={t("asset.typeDatabase")}>
        <DetailGrid>
          <InfoItem label={t("asset.driver")} value={driverLabel(loaded, cfg.driver)} />
          <InfoItem label={t("asset.host")} value={`${cfg.host}:${cfg.port}`} mono />
          <InfoItem label={t("asset.username")} value={cfg.username} mono />
          {cfg.database && <InfoItem label={t("asset.database")} value={cfg.database} mono />}
          {cfg.password && <InfoItem label={t("asset.password")} value={MASKED_SECRET} />}
          {cfg.ssl_mode && cfg.ssl_mode !== "disable" && <InfoItem label={t("asset.sslMode")} value={cfg.ssl_mode} />}
          {cfg.tls && <InfoItem label="TLS" value={ENABLED_VALUE} />}
          {cfg.read_only && <InfoItem label={t("asset.readOnly")} value={ENABLED_VALUE} />}
          {cfg.params && <InfoItem label={t("asset.params")} value={cfg.params} mono />}
          {descriptor &&
            driverOptionItems(descriptor, cfg.driver_options ?? {}).map(({ key, label, value, mono }) => (
              <InfoItem key={key} label={label} value={value} mono={mono} />
            ))}
        </DetailGrid>
        {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
      </DetailSection>
      <ProxyDetailSection proxy={cfg.proxy} />
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
    </>
  );
}

/**
 * 驱动专属字段里有值的那些,按声明顺序;口令类不列。哪些是口令只有驱动描述说得出,所以描述
 * 没取到或驱动不可用时一项也不列。标签取自描述(扩展已按自己的多语言解析)。
 */
function driverOptionItems(driver: DriverDescriptor, options: Record<string, unknown>) {
  return driverFieldsOf(driver)
    .filter((f) => f.type !== "password" && options[f.key] !== undefined && options[f.key] !== "")
    .map((f) => {
      const v = options[f.key];
      return {
        key: f.key,
        label: f.label || f.key,
        value: typeof v === "boolean" ? (v ? ENABLED_VALUE : DISABLED_VALUE) : String(v),
        mono: typeof v !== "boolean",
      };
    });
}

/** 驱动行显示描述里的展示名;描述还没取到或取失败、或注册表不认识该驱动时,显示存下来的驱动 ID。 */
function driverLabel(loaded: ReturnType<typeof useDatabaseDrivers>, id: string): string {
  if (loaded.status !== "ready") return id;
  return findDatabaseDriver(loaded.drivers, id)?.name ?? id;
}
