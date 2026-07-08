import { S3Icon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";
import { OSSDetailInfoCard } from "@/components/asset/detail/OSSDetailInfoCard";
import { OSSConfigSection } from "@/components/asset/OSSConfigSection";

registerAssetType({
  type: "oss",
  icon: S3Icon,
  aliases: ["oss"],
  label: "nav.oss",
  category: "databases",
  // P3b-1 对象浏览器已落地：canConnect 开启双击/右键「连接」→ 通用 query 路径(App.tsx :287)。
  // canConnectInNewTab 保持 false —— 与其它 query 资产一致；新标签路径 handleConnectAssetInNewTab
  // 仅走 terminal connect(),对 query 资产会误路由(见 plan T8 决策,需要新标签需先改造该 handler)。
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: OSSDetailInfoCard,
  ConfigSection: OSSConfigSection,
  testable: true,
  // 后端 PolicyKind()=="" / DefaultPolicy()==nil —— OSS 无 policy。
  policy: undefined,
});
