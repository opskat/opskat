import { useTranslation } from "react-i18next";
import { DetailGrid, DetailSection, InfoItem } from "@/components/asset/detail/InfoItem";
import { DISABLED_VALUE, ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "@/components/asset/detail/utils";
import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
import type { ExtensionConfigSchema } from "@/extension/configSchema";

interface Options {
  /** 扩展显示名的 i18n key，与它自己的 `ext-<name>` 命名空间一起解析。 */
  displayNameKey: string;
  ns: string;
  assetType: string;
  schema?: ExtensionConfigSchema;
}

/**
 * 扩展资产类型的详情卡：按 manifest 的 configSchema 渲染已保存的配置。
 *
 * 内置类型每种有一个手写的 DetailInfoCard；扩展类型的"手写卡"就是它的 configSchema，
 * 所以这里由 schema 生成一个，注册进同一个 DetailInfoCard 槽位。AssetDetail 因此不再
 * 需要一段只对扩展生效的内联渲染。
 */
export function makeExtensionDetailInfoCard(opts: Options) {
  function ExtensionDetailInfoCard({ asset }: DetailInfoCardProps) {
    const { t } = useTranslation();
    const props = opts.schema?.properties ?? {};
    const order = opts.schema?.propertyOrder;
    const keys = order ? order.filter((k) => k in props) : Object.keys(props);
    if (keys.length === 0) return null;

    const parsed = parseDetailConfig<Record<string, unknown>>(asset.Config) ?? {};
    return (
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
        </DetailGrid>
      </DetailSection>
    );
  }
  ExtensionDetailInfoCard.displayName = `ExtensionDetailInfoCard(${opts.assetType})`;
  return ExtensionDetailInfoCard;
}
