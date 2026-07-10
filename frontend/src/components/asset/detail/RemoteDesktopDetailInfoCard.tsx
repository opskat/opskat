import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
import { DISABLED_VALUE, ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";
import type { DetailInfoCardProps } from "@/lib/assetTypes";

interface RemoteDesktopDetailConfig {
  host?: string;
  port?: number;
  username?: string;
  domain?: string;
  password?: string;
  credential_id?: number;
  security_type?: string;
  screen_width?: number;
  screen_height?: number;
  color_depth?: number;
  ignore_cert?: boolean;
  file_ssh_asset_id?: number;
}

export function RemoteDesktopDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const cfg = parseDetailConfig<RemoteDesktopDetailConfig>(asset.Config) ?? {};
  const isRdp = asset.Type === "rdp";
  return (
    <DetailSection title={isRdp ? "RDP" : "VNC"}>
      <DetailGrid>
        <InfoItem label="Host" value={cfg.host || "-"} mono />
        <InfoItem label="Port" value={String(cfg.port || (isRdp ? 3389 : 5900))} mono />
        {cfg.username && <InfoItem label="Username" value={cfg.username} mono />}
        {isRdp && cfg.domain && <InfoItem label="Domain" value={cfg.domain} mono />}
        {(cfg.password || cfg.credential_id) && <InfoItem label="Password" value={MASKED_SECRET} />}
        {!isRdp && cfg.security_type && <InfoItem label="Security" value={cfg.security_type} />}
        {isRdp && (
          <>
            <InfoItem label="Resolution" value={`${cfg.screen_width || 1280} x ${cfg.screen_height || 720}`} mono />
            <InfoItem label="Color Depth" value={String(cfg.color_depth || 24)} mono />
            <InfoItem label="Ignore Cert" value={cfg.ignore_cert ? ENABLED_VALUE : DISABLED_VALUE} />
          </>
        )}
        <InfoItem
          label="File Channel"
          value={cfg.file_ssh_asset_id ? sshTunnelName(cfg.file_ssh_asset_id) || "-" : DISABLED_VALUE}
        />
      </DetailGrid>
    </DetailSection>
  );
}
