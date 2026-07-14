import { ScreenShare } from "lucide-react";
import { registerAssetType } from "./_register";
import { VNCDetailInfoCard } from "@/components/asset/detail/VNCDetailInfoCard";
import { VNCConfigSection } from "@/components/asset/VNCConfigSection";

registerAssetType({
  type: "vnc",
  icon: ScreenShare,
  aliases: ["vnc"],
  label: "nav.vnc",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "page",
  pageId: "vnc",
  pageIcon: "screen-share",
  DetailInfoCard: VNCDetailInfoCard,
  ConfigSection: VNCConfigSection,
  testable: true,
});
