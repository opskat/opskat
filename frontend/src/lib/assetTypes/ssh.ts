import { Monitor } from "lucide-react";
import { registerAssetType } from "./_register";
import { SSHDetailInfoCard } from "@/components/asset/detail/SSHDetailInfoCard";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";

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
