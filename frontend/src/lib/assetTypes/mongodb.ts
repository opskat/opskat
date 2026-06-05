import { MongodbIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";
import { MongoDBDetailInfoCard } from "@/components/asset/detail/MongoDBDetailInfoCard";
import { MongoDBConfigSection } from "@/components/asset/MongoDBConfigSection";

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
