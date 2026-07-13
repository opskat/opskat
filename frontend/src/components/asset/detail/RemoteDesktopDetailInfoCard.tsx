import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
import { DISABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";
import type { DetailInfoCardProps } from "@/lib/assetTypes";

interface RemoteDesktopDetailConfig {
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  credential_id?: number;
  security_type?: string;
  file_ssh_asset_id?: number;
}

export function RemoteDesktopDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const cfg = parseDetailConfig<RemoteDesktopDetailConfig>(asset.Config) ?? {};
  return (
    <DetailSection title="VNC">
      <DetailGrid>
        <InfoItem label="Host" value={cfg.host || "-"} mono />
        <InfoItem label="Port" value={String(cfg.port || 5900)} mono />
        {cfg.username && <InfoItem label="Username" value={cfg.username} mono />}
        {(cfg.password || cfg.credential_id) && <InfoItem label="Password" value={MASKED_SECRET} />}
        {cfg.security_type && <InfoItem label="Security" value={cfg.security_type} />}
        <InfoItem
          label="File Channel"
          value={cfg.file_ssh_asset_id ? sshTunnelName(cfg.file_ssh_asset_id) || "-" : DISABLED_VALUE}
        />
      </DetailGrid>
    </DetailSection>
  );
}
