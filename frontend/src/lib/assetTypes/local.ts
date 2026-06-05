import { SquareTerminal } from "lucide-react";
import { registerAssetType } from "./_register";
import { LocalDetailInfoCard } from "@/components/asset/detail/LocalDetailInfoCard";
import { LocalConfigSection } from "@/components/asset/LocalConfigSection";

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
