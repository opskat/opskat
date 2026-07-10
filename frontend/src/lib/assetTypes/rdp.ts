import { MonitorUp } from "lucide-react";
import { registerAssetType } from "./_register";
import { RemoteDesktopDetailInfoCard } from "@/components/asset/detail/RemoteDesktopDetailInfoCard";
import { RDPConfigSection } from "@/components/asset/RemoteDesktopConfigSection";

registerAssetType({
  type: "rdp",
  icon: MonitorUp,
  aliases: ["rdp"],
  label: "nav.rdp",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "remoteDesktop",
  DetailInfoCard: RemoteDesktopDetailInfoCard,
  ConfigSection: RDPConfigSection,
  testable: true,
});
