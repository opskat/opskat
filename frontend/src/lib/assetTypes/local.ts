import { lazy } from "react";
import { SquareTerminal } from "lucide-react";
import { registerAssetType } from "./_register";

const LocalDetailInfoCard = lazy(() =>
  import("@/components/asset/detail/LocalDetailInfoCard").then((m) => ({ default: m.LocalDetailInfoCard }))
);
const LocalConfigSection = lazy(() =>
  import("@/components/asset/LocalConfigSection").then((m) => ({ default: m.LocalConfigSection }))
);

registerAssetType({
  type: "local",
  icon: SquareTerminal,
  aliases: ["local", "shell", "terminal"],
  label: "nav.local",
  category: "servers",
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "terminal",
  DetailInfoCard: LocalDetailInfoCard,
  ConfigSection: LocalConfigSection,
  policy: undefined,
});
