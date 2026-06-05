import { KubernetesIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";
import { K8sDetailInfoCard } from "@/components/asset/detail/K8sDetailInfoCard";
import { K8sConfigSection } from "@/components/asset/K8sConfigSection";

registerAssetType({
  type: "k8s",
  icon: KubernetesIcon,
  aliases: ["k8s", "kubernetes"],
  label: "nav.k8s",
  category: "middleware",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "terminal",
  DetailInfoCard: K8sDetailInfoCard,
  ConfigSection: K8sConfigSection,
  policy: {
    policyType: "k8s",
    titleKey: "asset.k8sPolicy",
    hintKey: "asset.k8sPolicyHint",
    testPlaceholderKey: "asset.k8sPolicyTestPlaceholder",
    fields: [
      {
        key: "allow_list",
        labelKey: "asset.k8sPolicyAllowList",
        placeholderKey: "asset.k8sPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_list",
        labelKey: "asset.k8sPolicyDenyList",
        placeholderKey: "asset.k8sPolicyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
