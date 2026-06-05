import { KafkaIcon } from "@/components/asset/brand-icons";
import { registerAssetType } from "./_register";
import { KafkaDetailInfoCard } from "@/components/asset/detail/KafkaDetailInfoCard";
import { KafkaConfigSection } from "@/components/asset/KafkaConfigSection";

registerAssetType({
  type: "kafka",
  icon: KafkaIcon,
  aliases: ["kafka"],
  label: "nav.kafka",
  category: "middleware",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: KafkaDetailInfoCard,
  ConfigSection: KafkaConfigSection,
  testable: true,
  policy: {
    policyType: "kafka",
    titleKey: "asset.kafkaPolicy",
    hintKey: "asset.kafkaPolicyHint",
    testPlaceholderKey: "asset.policyTestKafkaPlaceholder",
    fields: [
      {
        key: "allow_list",
        labelKey: "asset.kafkaPolicyAllowList",
        placeholderKey: "asset.kafkaPolicyPlaceholder",
        variant: "allow",
      },
      {
        key: "deny_list",
        labelKey: "asset.kafkaPolicyDenyList",
        placeholderKey: "asset.kafkaPolicyPlaceholder",
        variant: "deny",
      },
    ],
  },
});
