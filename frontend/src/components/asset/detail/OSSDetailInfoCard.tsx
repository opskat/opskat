import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import { OSS_PROVIDER_LABEL_KEYS } from "../OSSConfigSection.config";
import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
import { DISABLED_VALUE, ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";

/** 只读 SafeView 白名单(见 internal/assettype/oss.go 的 SafeView);secret/credential_id 故意不在。 */
interface OSSConfig {
  provider?: string;
  endpoint?: string;
  region?: string;
  access_key_id?: string;
  secret_access_key?: string;
  use_path_style?: boolean;
  use_ssl?: boolean;
  connect_timeout?: number;
}

export function OSSDetailInfoCard({ asset }: DetailInfoCardProps) {
  const { t } = useTranslation();

  const cfg = parseDetailConfig<OSSConfig>(asset.Config);
  if (!cfg) return null;

  return (
    <DetailSection title={t("nav.oss")}>
      <DetailGrid>
        {cfg.provider && (
          <InfoItem label={t("oss.form.provider")} value={t(OSS_PROVIDER_LABEL_KEYS[cfg.provider] ?? cfg.provider)} />
        )}
        {cfg.endpoint && <InfoItem label={t("oss.form.endpoint")} value={cfg.endpoint} mono />}
        {cfg.region && <InfoItem label={t("oss.form.region")} value={cfg.region} mono />}
        {cfg.access_key_id && <InfoItem label={t("oss.form.accessKeyId")} value={cfg.access_key_id} mono />}
        {cfg.secret_access_key && <InfoItem label={t("oss.form.secretAccessKey")} value={MASKED_SECRET} />}
        <InfoItem label={t("oss.form.usePathStyle")} value={cfg.use_path_style ? ENABLED_VALUE : DISABLED_VALUE} />
        <InfoItem label={t("oss.form.useSSL")} value={cfg.use_ssl ? ENABLED_VALUE : DISABLED_VALUE} />
        {cfg.connect_timeout ? (
          <InfoItem label={t("oss.form.connectTimeout")} value={String(cfg.connect_timeout)} mono />
        ) : null}
      </DetailGrid>
    </DetailSection>
  );
}
