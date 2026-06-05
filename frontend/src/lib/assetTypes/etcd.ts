import { EtcdIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";
import { EtcdDetailInfoCard } from "@/components/asset/detail/EtcdDetailInfoCard";
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";

registerAssetType({
  type: "etcd",
  icon: EtcdIcon,
  aliases: ["etcd"],
  label: "nav.etcd",
  category: "databases",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: EtcdDetailInfoCard,
  ConfigSection: EtcdConfigSection,
  testable: true,
  policy: {
    policyType: "etcd",
    titleKey: "asset.etcdPolicy",
    hintKey: "asset.etcdPolicyHint",
    testPlaceholderKey: "asset.policyTestEtcdPlaceholder",
    fields: [
      {
        key: "allow_list",
        labelKey: "asset.etcdPolicyAllowList",
        placeholderKey: "asset.etcdPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_list",
        labelKey: "asset.etcdPolicyDenyList",
        placeholderKey: "asset.etcdPolicyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
