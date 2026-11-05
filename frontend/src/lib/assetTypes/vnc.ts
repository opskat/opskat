import { lazy } from "react";
import { ScreenShare } from "lucide-react";
import { registerAssetType } from "./_register";

const VNCDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/VNCDetailInfoCard").then((m) => ({ default: m.VNCDetailInfoCard }))
);
const VNCConfigSection = lazy(() =>
  import("@/components/asset/VNCConfigSection").then((m) => ({ default: m.VNCConfigSection }))
);

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
