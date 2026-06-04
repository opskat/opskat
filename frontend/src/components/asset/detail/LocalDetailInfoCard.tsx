import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
import { parseDetailConfig } from "./utils";
import { formatLocalShellArgs } from "@/lib/localShellArgs";

interface LocalConfig {
  shell?: string;
  args?: string[];
  cwd?: string;
}

export function LocalDetailInfoCard({ asset }: DetailInfoCardProps) {
  const { t } = useTranslation();

  const cfg = parseDetailConfig<LocalConfig>(asset.Config);
  if (!cfg) return null;

  return (
    <DetailSection title={t("asset.localTitle")}>
      <DetailGrid>
        <InfoItem label={t("asset.localShell")} value={cfg.shell || t("asset.localDefaultShell")} mono />
        {cfg.args && cfg.args.length > 0 && (
          <InfoItem label={t("asset.localArgs")} value={formatLocalShellArgs(cfg.args)} mono />
        )}
        <InfoItem label={t("asset.localCwd")} value={cfg.cwd || "~"} mono />
      </DetailGrid>
    </DetailSection>
  );
}
