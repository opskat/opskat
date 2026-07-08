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
  // 本期只做「新建/编辑/测试连接」。对象浏览器工作区是 P3:当前无连接目标,
  // canConnect:false 直接抑制 AssetTree 的双击连接与右键「连接」菜单。
  // connectAction 是必填(仅 terminal|query),填占位 "query"(canConnect:false 下不可达);
  // P3 落地浏览器后翻为 true 并在 App.tsx handleConnectAsset 加 oss 分支。
  canConnect: false,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: OSSDetailInfoCard,
  ConfigSection: OSSConfigSection,
  testable: true,
  // 后端 PolicyKind()=="" / DefaultPolicy()==nil —— OSS 无 policy。
  policy: undefined,
});
