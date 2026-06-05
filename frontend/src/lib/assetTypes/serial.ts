import { Usb } from "lucide-react";
import { registerAssetType } from "./_register";
import { SerialDetailInfoCard } from "@/components/asset/detail/SerialDetailInfoCard";
import { SerialConfigSection } from "@/components/asset/SerialConfigSection";

registerAssetType({
  type: "serial",
  icon: Usb,
  aliases: ["serial", "com", "tty"],
  label: "nav.serial",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "terminal",
  DetailInfoCard: SerialDetailInfoCard,
  ConfigSection: SerialConfigSection,
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
