import { useTranslation } from "react-i18next";
import {
  DetailGrid,
  DetailSection,
  InfoItem,
  ProxyChainDetailSection,
  TunnelInfo,
} from "@/components/asset/detail/InfoItem";
import { DISABLED_VALUE, ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "@/components/asset/detail/utils";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import type { ExtensionConfigSchema } from "@/extension/configSchema";
import type { ExtConnection } from "@/extension/types";
import { HOST_CONNECTION_CONFIG_KEY, type HostConnectionConfig } from "@/extension/connectionConfig";

interface Options {
  /** 扩展显示名的 i18n key，与它自己的 `ext-<name>` 命名空间一起解析。 */
  displayNameKey: string;
  ns: string;
  assetType: string;
  schema?: ExtensionConfigSchema;
  /** 资产类型声明的宿主连接配置；未声明的项即使资产上有值也不展示（宿主同样不应用）。 */
  connection?: ExtConnection;
}

/**
 * 扩展资产类型的详情卡：按 manifest 的 configSchema 渲染已保存的配置。
 *
 * 内置类型每种有一个手写的 DetailInfoCard；扩展类型的"手写卡"就是它的 configSchema，
 * 所以这里由 schema 生成一个，注册进同一个 DetailInfoCard 槽位。AssetDetail 因此不再
 * 需要一段只对扩展生效的内联渲染。
 */
export function makeExtensionDetailInfoCard(opts: Options) {
  function ExtensionDetailInfoCard({ asset, sshTunnelName }: DetailInfoCardProps) {
    const { t } = useTranslation();
    const props = opts.schema?.properties ?? {};
    const order = opts.schema?.propertyOrder;
    const keys = order ? order.filter((k) => k in props) : Object.keys(props);
    const tunnelName = opts.connection?.sshTunnel ? sshTunnelName(asset.sshTunnelId) : "";

    const parsed = parseDetailConfig<Record<string, unknown>>(asset.Config) ?? {};
    // 宿主保留键：与 SSH 隧道一样只展示声明了的项，未声明即使资产上有值也不展示。
    const hostConn = parsed[HOST_CONNECTION_CONFIG_KEY] as HostConnectionConfig | undefined;
    const chain = opts.connection?.proxyChain ? hostConn?.proxyChain : undefined;
    const tls = opts.connection?.tls ? hostConn?.tls : undefined;
    const hasChain = !!chain?.layers?.length;
    const hasTLS = !!tls?.enabled;
    if (keys.length === 0 && !tunnelName && !hasChain && !hasTLS) return null;

    return (
      <>
        <DetailSection title={t(opts.displayNameKey, { ns: opts.ns, defaultValue: opts.assetType })}>
          <DetailGrid>
            {keys.map((key) => {
              const prop = props[key];
              if (!prop) return null;
              const val = parsed[key];
              if (val === undefined || val === null || val === "") return null;
              return (
                <InfoItem
                  key={key}
                  label={prop.title || key}
                  value={
                    prop.format === "password"
                      ? MASKED_SECRET
                      : prop.type === "boolean"
                        ? val
                          ? ENABLED_VALUE
                          : DISABLED_VALUE
                        : String(val)
                  }
                />
              );
            })}
            {hasTLS && <InfoItem label={t("asset.tls")} value={ENABLED_VALUE} />}
          </DetailGrid>
          {tunnelName && <TunnelInfo label={t("asset.sshTunnel")} name={tunnelName} />}
        </DetailSection>
        <ProxyChainDetailSection chain={chain} resolveSshName={sshTunnelName} />
      </>
    );
  }
  ExtensionDetailInfoCard.displayName = `ExtensionDetailInfoCard(${opts.assetType})`;
  return ExtensionDetailInfoCard;
}
