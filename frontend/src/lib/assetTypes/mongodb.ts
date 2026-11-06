import { lazy } from "react";
import { MongodbIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";

const MongoDBDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/MongoDBDetailInfoCard").then((m) => ({ default: m.MongoDBDetailInfoCard }))
);
const MongoDBConfigSection = lazy(() =>
  import("@/components/asset/MongoDBConfigSection").then((m) => ({ default: m.MongoDBConfigSection }))
);

registerAssetType({
  type: "mongodb",
  icon: MongodbIcon,
  aliases: ["mongodb", "mongo"],
  label: "nav.mongodb",
  category: "databases",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: MongoDBDetailInfoCard,
  ConfigSection: MongoDBConfigSection,
  testable: true,
  policy: {
    policyType: "mongo",
    titleKey: "asset.mongoPolicy",
    hintKey: "asset.mongoPolicyHint",
    testPlaceholderKey: "asset.policyTestMongoPlaceholder",
    fields: [
      {
        key: "allow_types",
        labelKey: "asset.mongoPolicyAllowTypes",
        placeholderKey: "asset.mongoPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_types",
        labelKey: "asset.mongoPolicyDenyTypes",
        placeholderKey: "asset.mongoPolicyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
