import { ScreenShare } from "lucide-react";
import { registerAssetType } from "./_register";
import { RemoteDesktopDetailInfoCard } from "@/components/asset/detail/RemoteDesktopDetailInfoCard";
import { VNCConfigSection } from "@/components/asset/RemoteDesktopConfigSection";

registerAssetType({
  type: "vnc",
  icon: ScreenShare,
  aliases: ["vnc"],
  label: "nav.vnc",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "remoteDesktop",
  DetailInfoCard: RemoteDesktopDetailInfoCard,
  ConfigSection: VNCConfigSection,
  testable: true,
});
