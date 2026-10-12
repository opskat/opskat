import { useTranslation } from "react-i18next";
import { findDatabaseDriver, useDatabaseDrivers } from "@/lib/dbDrivers";
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
import { ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";

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
}

export function DatabaseDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();
  const loaded = useDatabaseDrivers();

  const cfg = parseDetailConfig<DatabaseConfig>(asset.Config);
  if (!cfg) return null;
  const tunnelName = sshTunnelName(asset.sshTunnelId || cfg.ssh_asset_id);

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
        </DetailGrid>
        {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
      </DetailSection>
      <ProxyDetailSection proxy={cfg.proxy} />
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
    </>
  );
}

/** 驱动行显示描述里的展示名;描述还没取到或取失败、或注册表不认识该驱动时,显示存下来的驱动 ID。 */
function driverLabel(loaded: ReturnType<typeof useDatabaseDrivers>, id: string): string {
  if (loaded.status !== "ready") return id;
  return findDatabaseDriver(loaded.drivers, id)?.name ?? id;
}
