import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import { InfoItem } from "./InfoItem";

interface K8sConfig {
  kubeconfig?: string;
  api_server?: string;
  token?: string;
  namespace?: string;
  context?: string;
  ssh_asset_id?: number;
}

export function K8sDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();

  let cfg: K8sConfig | null = null;
  try {
    cfg = JSON.parse(asset.Config || "{}");
  } catch {
    /* ignore */
  }
  if (!cfg) return null;
  const tunnelID = asset.sshTunnelId || cfg.ssh_asset_id;
  const tunnelName = sshTunnelName(tunnelID);

  return (
    <div className="rounded-xl border bg-card p-4">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">K8S</h3>
      <div className="grid grid-cols-2 gap-4 text-sm">
        <InfoItem label={t("asset.host")} value={cfg.api_server || t("asset.k8sKubeconfigProvided") || ""} mono />
        {cfg.namespace && <InfoItem label={t("asset.k8sNamespace")} value={cfg.namespace} mono />}
        {cfg.context && <InfoItem label={t("asset.k8sContext")} value={cfg.context} mono />}
        {cfg.token && <InfoItem label={t("asset.k8sToken")} value={"\u25CF\u25CF\u25CF\u25CF\u25CF\u25CF"} />}
      </div>
      {tunnelName && (
        <div className="mt-3 pt-3 border-t text-sm">
          <InfoItem label={t("asset.sshTunnel")} value={tunnelName} mono />
        </div>
      )}
    </div>
  );
}
