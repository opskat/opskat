import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
import { DISABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";
import type { DetailInfoCardProps } from "@/lib/assetTypes";

interface VNCDetailConfig {
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  credential_id?: number;
  file_ssh_asset_id?: number;
}

export function VNCDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const cfg = parseDetailConfig<VNCDetailConfig>(asset.Config) ?? {};
  return (
    <DetailSection title="VNC">
      <DetailGrid>
        <InfoItem label="Host" value={cfg.host || "-"} mono />
        <InfoItem label="Port" value={String(cfg.port || 5900)} mono />
        {cfg.username && <InfoItem label="Username" value={cfg.username} mono />}
        {(cfg.password || cfg.credential_id) && <InfoItem label="Password" value={MASKED_SECRET} />}
        <InfoItem
          label="File Channel"
          value={cfg.file_ssh_asset_id ? sshTunnelName(cfg.file_ssh_asset_id) || "-" : DISABLED_VALUE}
        />
      </DetailGrid>
    </DetailSection>
  );
}
