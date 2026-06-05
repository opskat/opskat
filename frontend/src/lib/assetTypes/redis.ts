import { RedisIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";
import { RedisDetailInfoCard } from "@/components/asset/detail/RedisDetailInfoCard";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";

registerAssetType({
  type: "redis",
  icon: RedisIcon,
  aliases: ["redis"],
  label: "nav.redis",
  category: "databases",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: RedisDetailInfoCard,
  ConfigSection: RedisConfigSection,
  testable: true,
  policy: {
    policyType: "redis",
    titleKey: "asset.redisPolicy",
    hintKey: "asset.redisPolicyHint",
    testPlaceholderKey: "asset.policyTestRedisPlaceholder",
    fields: [
      {
        key: "allow_list",
        labelKey: "asset.redisPolicyAllowList",
        placeholderKey: "asset.redisPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_list",
        labelKey: "asset.redisPolicyDenyList",
        placeholderKey: "asset.redisPolicyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
