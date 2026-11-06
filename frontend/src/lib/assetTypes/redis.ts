import { lazy } from "react";
import { RedisIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";

const RedisDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/RedisDetailInfoCard").then((m) => ({ default: m.RedisDetailInfoCard }))
);
const RedisConfigSection = lazy(() =>
  import("@/components/asset/RedisConfigSection").then((m) => ({ default: m.RedisConfigSection }))
);

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
