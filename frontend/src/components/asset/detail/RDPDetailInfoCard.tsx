import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
import { parseDetailConfig } from "./utils";

interface RDPConfig {
  host?: string;
  port?: number;
  username?: string;
  domain?: string;
  width?: number;
  height?: number;
  clipboard?: boolean;
}

export function RDPDetailInfoCard({ asset }: DetailInfoCardProps) {
  const { t } = useTranslation();
  const cfg = parseDetailConfig<RDPConfig>(asset.Config);
  if (!cfg) return null;

  return (
    <DetailSection title={t("asset.rdpTitle")}>
      <DetailGrid>
        {cfg.host && <InfoItem label={t("asset.host")} value={`${cfg.host}:${cfg.port || 3389}`} mono />}
        {cfg.username && <InfoItem label={t("asset.username")} value={cfg.username} mono />}
        {cfg.domain && <InfoItem label={t("asset.rdpDomain")} value={cfg.domain} mono />}
        {cfg.width && cfg.height && <InfoItem label={t("asset.rdpDisplay")} value={`${cfg.width} x ${cfg.height}`} mono />}
        <InfoItem
          label={t("asset.rdpClipboard")}
          value={cfg.clipboard === false ? t("asset.rdpClipboardOff") : t("asset.rdpClipboardOn")}
        />
      </DetailGrid>
    </DetailSection>
  );
}
