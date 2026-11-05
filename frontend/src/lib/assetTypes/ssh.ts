import { lazy } from "react";
import { Monitor } from "lucide-react";
import { registerAssetType } from "./_register";

const SSHDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/SSHDetailInfoCard").then((m) => ({ default: m.SSHDetailInfoCard }))
);
const SSHConfigSection = lazy(() =>
  import("@/components/asset/SSHConfigSection").then((m) => ({ default: m.SSHConfigSection }))
);

registerAssetType({
  type: "ssh",
  icon: Monitor,
  aliases: ["ssh"],
  label: "nav.ssh",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "terminal",
  canOpenFileManager: true,
  DetailInfoCard: SSHDetailInfoCard,
  ConfigSection: SSHConfigSection,
  testable: true,
  policy: {
    policyType: "ssh",
    titleKey: "asset.cmdPolicy",
    hintKey: "asset.cmdPolicyHint",
    testPlaceholderKey: "asset.policyTestPlaceholder",
    fields: [
      {
        key: "allow_list",
        labelKey: "asset.cmdPolicyAllowList",
        placeholderKey: "asset.cmdPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_list",
        labelKey: "asset.cmdPolicyDenyList",
        placeholderKey: "asset.cmdPolicyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
