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
  // 集群面板是一个 page，不是终端。这条曾经写成 "terminal"，靠 openAsset 里一个
  // `asset.Type === "k8s"` 硬编码分支把它改道到 page —— 注册表说的和实际发生的不一致。
  connectAction: "page",
  pageId: "k8s-cluster",
  // tab id 保持历史形态 `k8s-<assetID>`（持久化在 localStorage 里），页面 id 是
  // "k8s-cluster"（MainPanel 按它渲染 K8sClusterPage）。
  pageTabPrefix: "k8s",
  pageIcon: "kubernetes",
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
