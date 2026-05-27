import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import { DetailGrid, DetailSection, InfoItem, TunnelInfo } from "./InfoItem";
import { ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";

interface EtcdConfig {
  endpoints?: string[];
  username?: string;
  password?: string;
  tls?: boolean;
  tls_insecure?: boolean;
  dial_timeout_seconds?: number;
  command_timeout_seconds?: number;
  ssh_asset_id?: number;
}

export function EtcdDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();

  const cfg = parseDetailConfig<EtcdConfig>(asset.Config);
  if (!cfg) return null;
  const tunnelName = sshTunnelName(asset.sshTunnelId || cfg.ssh_asset_id);
  const endpoints = (cfg.endpoints || []).join(", ");

  return (
    <DetailSection title="etcd">
      <DetailGrid>
        <InfoItem label={t("etcd.form.endpoints")} value={endpoints} mono />
        {cfg.username && <InfoItem label={t("asset.username")} value={cfg.username} mono />}
        {cfg.password && <InfoItem label={t("asset.password")} value={MASKED_SECRET} />}
        {cfg.tls && <InfoItem label={t("asset.tls")} value={ENABLED_VALUE} />}
        {cfg.dial_timeout_seconds !== undefined && (
          <InfoItem label={t("etcd.form.dialTimeout")} value={String(cfg.dial_timeout_seconds)} mono />
        )}
        {cfg.command_timeout_seconds !== undefined && (
          <InfoItem label={t("etcd.form.commandTimeout")} value={String(cfg.command_timeout_seconds)} mono />
        )}
      </DetailGrid>
      {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
    </DetailSection>
  );
}
