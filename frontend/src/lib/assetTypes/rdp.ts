import { Monitor } from "lucide-react";
import { registerAssetType } from "./_register";
import { RDPDetailInfoCard } from "@/components/asset/detail/RDPDetailInfoCard";
import { RDPConfigSection } from "@/components/asset/RDPConfigSection";

registerAssetType({
  type: "rdp",
  icon: Monitor,
  aliases: ["rdp"],
  label: "nav.rdp",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "page",
  pageId: "rdp",
  pageIcon: "monitor",
  DetailInfoCard: RDPDetailInfoCard,
  ConfigSection: RDPConfigSection,
  testable: true,
  policy: undefined,
});
