import { Database } from "lucide-react";
import { registerAssetType } from "./_register";
import { EtcdDetailInfoCard } from "@/components/asset/detail/EtcdDetailInfoCard";

registerAssetType({
  type: "etcd",
  icon: Database,
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: EtcdDetailInfoCard,
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
