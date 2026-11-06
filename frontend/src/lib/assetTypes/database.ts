import { lazy } from "react";
import { Database } from "lucide-react";
import { registerAssetType } from "./_register";

const DatabaseDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/DatabaseDetailInfoCard").then((m) => ({ default: m.DatabaseDetailInfoCard }))
);
const DatabaseConfigSection = lazy(() =>
  import("@/components/asset/DatabaseConfigSection").then((m) => ({ default: m.DatabaseConfigSection }))
);

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
