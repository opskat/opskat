import { useTranslation } from "react-i18next";
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

interface RedisConfig {
  host: string;
  port: number;
  username?: string;
  password?: string;
  database?: number;
  tls?: boolean;
  ssh_asset_id?: number;
  proxy?: ProxyConfigJSON | null;
  proxy_chain?: ProxyChainJSON | null;
  mode?: string;
  nodes?: string[];
  master_name?: string;
  sentinel_username?: string;
  sentinel_password?: string;
  node_address_map?: Record<string, string>;
}

export function RedisDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();

  const cfg = parseDetailConfig<RedisConfig>(asset.Config);
  if (!cfg) return null;
  const tunnelName = sshTunnelName(asset.sshTunnelId || cfg.ssh_asset_id);
  const mode = cfg.mode === "cluster" || cfg.mode === "sentinel" ? cfg.mode : "standalone";
  const nodeAddressMapEntries = Object.entries(cfg.node_address_map || {});

  return (
    <>
      <DetailSection title="Redis">
        <DetailGrid>
          {mode === "standalone" && <InfoItem label={t("asset.host")} value={`${cfg.host}:${cfg.port}`} mono />}
          {mode === "cluster" && (
            <InfoItem label={t("asset.redisClusterNodes")} value={(cfg.nodes || []).join(", ")} mono />
          )}
          {mode === "sentinel" && (
            <>
              <InfoItem label={t("asset.redisMasterName")} value={cfg.master_name || ""} mono />
              <InfoItem label={t("asset.redisSentinelNodes")} value={(cfg.nodes || []).join(", ")} mono />
            </>
          )}
          {cfg.username && (
            <InfoItem
              label={mode === "sentinel" ? t("asset.redisDataNodeUsername") : t("asset.username")}
              value={cfg.username}
              mono
            />
          )}
          {cfg.password && <InfoItem label={t("asset.password")} value={MASKED_SECRET} />}
          {mode !== "cluster" && <InfoItem label={t("asset.redisDatabase")} value={String(cfg.database || 0)} mono />}
          {cfg.tls && <InfoItem label={t("asset.tls")} value={ENABLED_VALUE} />}
          {mode === "sentinel" && cfg.sentinel_username && (
            <InfoItem label={t("asset.redisSentinelUsername")} value={cfg.sentinel_username} mono />
          )}
          {mode === "sentinel" && cfg.sentinel_password && (
            <InfoItem label={t("asset.redisSentinelPassword")} value={MASKED_SECRET} />
          )}
        </DetailGrid>
        {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
        {nodeAddressMapEntries.length > 0 && (
          <div className="mt-3 border-t pt-3 text-sm">
            <InfoItem
              label={t("asset.redisNodeAddressMap")}
              value={nodeAddressMapEntries.map(([k, v]) => `${k} = ${v}`).join(", ")}
              mono
            />
          </div>
        )}
      </DetailSection>
      <ProxyDetailSection proxy={cfg.proxy} />
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
    </>
  );
}
