import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import type { ProxyConfigJSON, ProxyChainJSON } from "../proxyConfig";
import { DetailGrid, DetailSection, InfoItem, ProxyChainDetailSection, ProxyDetailSection } from "./InfoItem";
import { parseDetailConfig } from "./utils";
import { GetAgentAssetDetail } from "../../../../wailsjs/go/system/System";
import { system as system_models } from "../../../../wailsjs/go/models";

interface SSHConfig {
  host: string;
  port: number;
  username: string;
  auth_type: string;
  password?: string;
  credential_id?: number;
  private_keys?: string[];
  agent_source_id?: number;
  agent_key_fingerprint?: string;
  jump_host_id?: number;
  proxy?: ProxyConfigJSON | null;
  proxy_chain?: ProxyChainJSON | null;
}

// 详情可用性文案;未知(读取失败/未加载)显示占位符,不编造运行时状态。
function availabilityLabel(t: TFunction, availability?: string): string {
  switch (availability) {
    case "ok":
      return t("agentSource.statusOk");
    case "empty":
      return t("agentSource.statusEmpty");
    case "unavailable":
      return t("agentSource.statusUnavailable");
    case "unsupported":
      return t("agentSource.statusUnsupported");
    case "missing":
      return t("asset.agentIdentityMissing");
    default:
      return "—";
  }
}

export function SSHDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
  const { t } = useTranslation();

  const cfg = parseDetailConfig<SSHConfig>(asset.Config);
  const [agentDetail, setAgentDetail] = useState<system_models.AgentAssetDetail | null>(null);

  // Agent 认证详情:仅按需读取所选 Agent 信息(来源名/已存指纹/当前可用性/可用时的类型与备注),
  // 不读取来源定义 —— 详情绝不暴露端点或公钥。
  useEffect(() => {
    const parsed = parseDetailConfig<SSHConfig>(asset.Config);
    if (parsed?.auth_type !== "agent" || !parsed.agent_source_id || !parsed.agent_key_fingerprint) {
      // 非 agent:详情段不渲染,无需在此同步清态。
      return;
    }
    let cancelled = false;
    GetAgentAssetDetail(parsed.agent_source_id, parsed.agent_key_fingerprint)
      .then((d) => {
        if (!cancelled) setAgentDetail(d);
      })
      .catch(() => {
        if (!cancelled) setAgentDetail(null);
      });
    return () => {
      cancelled = true;
    };
  }, [asset.Config]);

  if (!cfg) return null;

  const jumpHostName = sshTunnelName(asset.sshTunnelId || cfg.jump_host_id);

  return (
    <>
      {/* SSH Connection Info */}
      <DetailSection title="SSH Connection">
        <DetailGrid>
          <InfoItem label={t("asset.host")} value={cfg.host} mono />
          <InfoItem label={t("asset.port")} value={String(cfg.port)} mono />
          <InfoItem label={t("asset.username")} value={cfg.username} mono />
          <InfoItem
            label={t("asset.authType")}
            value={
              cfg.auth_type === "password"
                ? t("asset.authPassword") + (cfg.password ? " \u25CF" : "")
                : cfg.auth_type === "key"
                  ? t("asset.authKey") +
                    (cfg.credential_id
                      ? ` (${t("asset.keySourceManaged")})`
                      : cfg.private_keys?.length
                        ? ` (${t("asset.keySourceFile")})`
                        : "")
                  : cfg.auth_type === "agent"
                    ? t("asset.authAgent")
                    : cfg.auth_type
            }
          />
        </DetailGrid>
      </DetailSection>

      {/* SSH Agent (auth_type=agent) 详情:认证类型/来源名/已存指纹/当前可用性;
          密钥类型与备注仅在所选身份当前可用时显示,身份缺失不展示过期元数据。 */}
      {cfg.auth_type === "agent" && (
        <DetailSection title={t("asset.agentDetailSection")}>
          <DetailGrid>
            <InfoItem label={t("asset.authType")} value={t("asset.authAgent")} />
            <InfoItem
              label={t("asset.agentSource")}
              value={agentDetail?.source_name || (cfg.agent_source_id ? `#${cfg.agent_source_id}` : "—")}
            />
            <InfoItem label={t("agentSource.fingerprint")} value={cfg.agent_key_fingerprint || "—"} mono />
            <InfoItem label={t("asset.agentAvailability")} value={availabilityLabel(t, agentDetail?.availability)} />
            {agentDetail?.availability === "ok" && (
              <>
                <InfoItem label={t("asset.agentKeyType")} value={agentDetail.type || "—"} />
                <InfoItem label={t("asset.agentComment")} value={agentDetail.comment || "—"} />
              </>
            )}
          </DetailGrid>
        </DetailSection>
      )}

      {/* SSH Private Keys */}
      {cfg.private_keys && cfg.private_keys.length > 0 && (
        <DetailSection title={t("asset.privateKeys")}>
          <div className="flex flex-col gap-1">
            {cfg.private_keys.map((key, i) => (
              <p key={i} className="text-sm font-mono text-muted-foreground">
                {key}
              </p>
            ))}
          </div>
        </DetailSection>
      )}

      {/* SSH Jump Host */}
      {jumpHostName && (
        <DetailSection title={t("asset.jumpHost")}>
          <p className="text-sm font-mono">{jumpHostName}</p>
        </DetailSection>
      )}

      {/* SSH Proxy */}
      <ProxyDetailSection proxy={cfg.proxy} />
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
    </>
  );
}
