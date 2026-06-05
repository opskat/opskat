import { Database } from "lucide-react";
import { registerAssetType } from "./_register";
import { DatabaseDetailInfoCard } from "@/components/asset/detail/DatabaseDetailInfoCard";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";

registerAssetType({
  type: "database",
  icon: Database,
  aliases: ["database", "mysql", "postgresql"],
  label: "nav.database",
  category: "databases",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: DatabaseDetailInfoCard,
  ConfigSection: DatabaseConfigSection,
  testable: true,
  policy: {
    policyType: "database",
    titleKey: "asset.queryPolicy",
    hintKey: "asset.queryPolicyHint",
    testPlaceholderKey: "asset.policyTestSqlPlaceholder",
    fields: [
      {
        key: "allow_types",
        labelKey: "asset.queryPolicyAllowTypes",
        placeholderKey: "asset.queryPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_types",
        labelKey: "asset.queryPolicyDenyTypes",
        placeholderKey: "asset.queryPolicyPlaceholder",
        variant: "deny",
      },
      {
        key: "deny_flags",
        labelKey: "asset.queryPolicyDenyFlags",
        placeholderKey: "asset.queryPolicyFlagPlaceholder",
        variant: "warn",
      },
    ],
  },
});
