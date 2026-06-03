import { SquareTerminal } from "lucide-react";
import { registerAssetType } from "./_register";
import { LocalDetailInfoCard } from "@/components/asset/detail/LocalDetailInfoCard";

registerAssetType({
  type: "local",
  icon: SquareTerminal,
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "terminal",
  DetailInfoCard: LocalDetailInfoCard,
  policy: undefined,
});
