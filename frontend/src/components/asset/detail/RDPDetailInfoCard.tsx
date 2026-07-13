import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import type { ProxyConfigJSON } from "../proxyConfig";
import { DetailGrid, DetailSection, InfoItem, ProxyDetailSection, TunnelInfo } from "./InfoItem";
import { parseDetailConfig } from "./utils";

interface RDPConfig {
  host?: string;
  port?: number;
  username?: string;
  domain?: string;
  clipboard?: boolean;
  ssh_asset_id?: number;
  proxy?: ProxyConfigJSON | null;
}

export function RDPDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();
  const cfg = parseDetailConfig<RDPConfig>(asset.Config);
  if (!cfg) return null;
  const tunnelName = sshTunnelName(asset.sshTunnelId || cfg.ssh_asset_id);

  return (
    <>
      <DetailSection title={t("asset.rdpTitle")}>
        <DetailGrid>
          {cfg.host && <InfoItem label={t("asset.host")} value={`${cfg.host}:${cfg.port || 3389}`} mono />}
          {cfg.username && <InfoItem label={t("asset.username")} value={cfg.username} mono />}
          {cfg.domain && <InfoItem label={t("asset.rdpDomain")} value={cfg.domain} mono />}
          <InfoItem
            label={t("asset.rdpClipboard")}
            value={cfg.clipboard === false ? t("asset.rdpClipboardOff") : t("asset.rdpClipboardOn")}
          />
        </DetailGrid>
        {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
      </DetailSection>
      <ProxyDetailSection proxy={cfg.proxy} />
    </>
  );
}
