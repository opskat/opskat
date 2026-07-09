import { Monitor } from "lucide-react";
import { registerAssetType } from "./_register";
import { RDPDetailInfoCard } from "@/components/asset/detail/RDPDetailInfoCard";
import { RDPConfigSection } from "@/components/asset/RDPConfigSection";

registerAssetType({
  type: "rdp",
  icon: Monitor,
  aliases: ["rdp", "remote-desktop"],
  label: "nav.rdp",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "page",
  pageId: "rdp",
  pageIcon: "monitor",
  DetailInfoCard: RDPDetailInfoCard,
  ConfigSection: RDPConfigSection,
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
