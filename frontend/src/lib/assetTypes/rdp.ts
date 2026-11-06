import { lazy } from "react";
import { Monitor } from "lucide-react";
import { registerAssetType } from "./_register";

const RDPDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/RDPDetailInfoCard").then((m) => ({ default: m.RDPDetailInfoCard }))
);
const RDPConfigSection = lazy(() =>
  import("@/components/asset/RDPConfigSection").then((m) => ({ default: m.RDPConfigSection }))
);

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
