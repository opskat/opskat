import { useTranslation } from "react-i18next";
import { type KafkaTabState, type KafkaTopicSummary } from "@/stores/kafkaStore";
import { EmptyState, LoadingBlock, Metric, Info } from "./shared";

export function OverviewView({ state }: { state: KafkaTabState }) {
  const { t } = useTranslation();
  const overview = state.overview;
  if (state.loadingOverview && !overview) return <LoadingBlock />;
  if (!overview) return <EmptyState text={t("query.kafkaNoOverview")} />;

  const controller = state.brokers.find((broker) => broker.nodeId === overview.controllerId);

  return (
    <div className="space-y-4 p-4">
      <div className="grid gap-3 md:grid-cols-4">
        <Metric label={t("query.kafkaBrokerCount")} value={overview.brokerCount} />
        <Metric label={t("query.kafkaTopicCount")} value={overview.topicCount} />
        <Metric label={t("query.kafkaPartitionCount")} value={overview.partitionCount} />
        <Metric label={t("query.kafkaUnderReplicated")} value={overview.underReplicatedPartitionCount} />
      </div>
      <div className="rounded-md border">
        <div className="grid grid-cols-2 gap-x-6 gap-y-3 p-4 text-sm md:grid-cols-4">
          <Info label={t("query.kafkaClusterId")} value={overview.clusterId || "-"} mono />
          <Info label={t("query.kafkaController")} value={String(overview.controllerId)} mono />
          <Info
            label={t("query.kafkaControllerHost")}
            value={controller ? `${controller.host}:${controller.port}` : "-"}
            mono
          />
          <Info label={t("query.kafkaInternalTopics")} value={String(overview.internalTopicCount)} mono />
          <Info label={t("query.kafkaOfflinePartitions")} value={String(overview.offlinePartitionCount)} mono />
        </div>
      </div>
      <TopicHealthTable topics={state.topics.slice(0, 8)} />
    </div>
  );
}

function TopicHealthTable({ topics }: { topics: KafkaTopicSummary[] }) {
  const { t } = useTranslation();
  if (!topics.length) return null;
  return (
    <div className="rounded-md border">
      <div className="border-b px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {t("query.kafkaRecentTopics")}
      </div>
      <table className="w-full text-sm">
        <thead className="bg-muted/40 text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 text-left font-medium">{t("query.kafkaTopic")}</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaPartitions")}</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaReplicationFactor")}</th>
            <th className="px-3 py-2 text-right font-medium">{t("query.kafkaUnderReplicated")}</th>
          </tr>
        </thead>
        <tbody>
          {topics.map((topic) => (
            <tr key={topic.name} className="border-t">
              <td className="max-w-[360px] truncate px-3 py-2 font-mono text-xs">{topic.name}</td>
              <td className="px-3 py-2 text-right tabular-nums">{topic.partitionCount}</td>
              <td className="px-3 py-2 text-right tabular-nums">{topic.replicationFactor}</td>
              <td className="px-3 py-2 text-right tabular-nums">{topic.underReplicatedPartitionCount}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
