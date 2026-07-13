import { useTranslation } from "react-i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import type { ProxyChainJSON } from "../proxyConfig";
import { DetailGrid, DetailSection, InfoItem, ProxyChainDetailSection, TunnelInfo } from "./InfoItem";
import { parseDetailConfig } from "./utils";

interface K8sConfig {
  kubeconfig?: string;
  namespace?: string;
  context?: string;
  proxy_chain?: ProxyChainJSON | null;
}

export function K8sDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();

  const cfg = parseDetailConfig<K8sConfig>(asset.Config);
  if (!cfg) return null;
  const tunnelName = sshTunnelName(asset.sshTunnelId);

  return (
    <>
      <DetailSection title="K8S">
        <DetailGrid>
          <InfoItem
            label={t("asset.k8sKubeconfig")}
            value={cfg.kubeconfig ? t("asset.k8sKubeconfigProvided") : ""}
            mono
          />
          {cfg.namespace && <InfoItem label={t("asset.k8sNamespace")} value={cfg.namespace} mono />}
          {cfg.context && <InfoItem label={t("asset.k8sContext")} value={cfg.context} mono />}
        </DetailGrid>
        {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
      </DetailSection>
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
    </>
  );
}
