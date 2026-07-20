import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Search, Settings } from "lucide-react";
import { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Input } from "@opskat/ui";
import { type KafkaTabState, useKafkaStore } from "@/stores/kafkaStore";
import { EmptyState, LoadingBlock } from "./shared";

export function BrokersView({ tabId, state }: { tabId: string; state: KafkaTabState }) {
  const { t } = useTranslation();
  const [clusterConfigOpen, setClusterConfigOpen] = useState(false);
  const loadBrokerConfig = useKafkaStore((s) => s.loadBrokerConfig);
  const loadClusterConfigs = useKafkaStore((s) => s.loadClusterConfigs);

  const handleSelectBroker = (brokerId: number) => {
    loadBrokerConfig(tabId, brokerId);
  };

  const handleClusterConfig = () => {
    setClusterConfigOpen(true);
    loadClusterConfigs(tabId);
  };

  if (state.loadingBrokers && !state.brokers.length) return <LoadingBlock />;
  if (!state.brokers.length) return <EmptyState text={t("query.kafkaNoBrokers")} />;

  const showDetail = state.selectedBroker !== undefined;

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b px-4 py-2">
        <span className="text-xs text-muted-foreground">
          {t("query.kafkaBrokerTotal", { count: state.brokers.length })}
        </span>
        <Button variant="outline" size="sm" className="ml-auto h-8 gap-1.5" onClick={handleClusterConfig}>
          <Settings className="h-3.5 w-3.5" />
          {t("query.kafkaClusterConfig")}
        </Button>
      </div>
      <div
        className={`grid min-h-0 flex-1 ${showDetail ? "grid-cols-[minmax(320px,1fr)_minmax(300px,0.8fr)]" : "grid-cols-1"}`}
      >
        <div className="min-h-0 overflow-auto">
          <div className="p-4">
            <div className="overflow-hidden rounded-md border">
              <table className="w-full text-sm">
                <thead className="bg-muted/40 text-xs text-muted-foreground">
                  <tr>
                    <th className="px-3 py-2 text-left font-medium">{t("query.kafkaBrokerId")}</th>
                    <th className="px-3 py-2 text-left font-medium">{t("asset.host")}</th>
                    <th className="px-3 py-2 text-right font-medium">{t("asset.port")}</th>
                    <th className="px-3 py-2 text-left font-medium">Rack</th>
                  </tr>
                </thead>
                <tbody>
                  {state.brokers.map((broker) => (
                    <tr
                      key={broker.nodeId}
                      className={`cursor-pointer border-t hover:bg-muted/30 ${state.selectedBroker === broker.nodeId ? "bg-muted/50" : ""}`}
                      onClick={() => handleSelectBroker(broker.nodeId)}
                    >
                      <td className="px-3 py-2 font-mono text-xs">{broker.nodeId}</td>
                      <td className="px-3 py-2 font-mono text-xs">{broker.host}</td>
                      <td className="px-3 py-2 text-right font-mono text-xs">{broker.port}</td>
                      <td className="px-3 py-2 text-muted-foreground">{broker.rack || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
        {showDetail && (
          <div className="min-h-0 overflow-auto border-l">
            <BrokerConfigPanel
              state={state}
              onClose={() =>
                useKafkaStore.setState((s) => ({
                  states: {
                    ...s.states,
                    [tabId]: { ...s.states[tabId], selectedBroker: undefined, brokerConfig: undefined },
                  },
                }))
              }
            />
          </div>
        )}
      </div>

      <Dialog open={clusterConfigOpen} onOpenChange={setClusterConfigOpen}>
        <DialogContent className="max-h-[80vh] max-w-3xl overflow-hidden flex flex-col">
          <DialogHeader>
            <DialogTitle>{t("query.kafkaClusterConfig")}</DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-auto">
            {state.loadingClusterConfigs ? (
              <LoadingBlock />
            ) : state.clusterConfigs?.error ? (
              <div className="flex items-center gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
                <AlertCircle className="h-4 w-4 shrink-0" />
                {state.clusterConfigs.error}
              </div>
            ) : (
              <ConfigTable configs={state.clusterConfigs?.configs || []} />
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setClusterConfigOpen(false)}>
              {t("action.close")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function BrokerConfigPanel({ state, onClose }: { state: KafkaTabState; onClose: () => void }) {
  const { t } = useTranslation();
  const broker = state.brokers.find((b) => b.nodeId === state.selectedBroker);

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center justify-between border-b px-3 py-2">
        <span className="text-sm font-medium">
          {t("query.kafkaBrokerConfig")} — {broker ? `${broker.host}:${broker.port}` : `#${state.selectedBroker}`}
        </span>
        <Button variant="ghost" size="sm" className="h-6 w-6 p-0" onClick={onClose}>
          ✕
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-3">
        {state.loadingBrokerConfig ? (
          <LoadingBlock />
        ) : state.brokerConfig?.error ? (
          <div className="flex items-center gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
            <AlertCircle className="h-4 w-4 shrink-0" />
            {state.brokerConfig.error}
          </div>
        ) : (
          <ConfigTable configs={state.brokerConfig?.configs || []} />
        )}
      </div>
    </div>
  );
}

function ConfigTable({
  configs,
}: {
  configs: { name: string; value?: string; isSensitive: boolean; source?: string }[];
}) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const filtered = configs.filter((c) => !search || c.name.toLowerCase().includes(search.toLowerCase()));

  if (!configs.length) return <EmptyState text={t("query.kafkaNoConfigs")} />;

  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          className="h-8 pl-7 text-sm"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t("query.kafkaFilterConfigs")}
        />
      </div>
      <div className="overflow-hidden rounded-md border">
        <table className="w-full text-sm">
          <thead className="bg-muted/40 text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 text-left font-medium">{t("asset.name")}</th>
              <th className="px-3 py-2 text-left font-medium">{t("query.kafkaConfigValue")}</th>
              <th className="px-3 py-2 text-left font-medium">{t("query.kafkaConfigSource")}</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((cfg) => (
              <tr key={cfg.name} className="border-t">
                <td className="px-3 py-2 font-mono text-xs">{cfg.name}</td>
                <td className="max-w-[200px] truncate px-3 py-2 font-mono text-xs">
                  {cfg.isSensitive ? (
                    <span className="text-muted-foreground italic">{t("query.kafkaConfigSensitive")}</span>
                  ) : cfg.value !== undefined ? (
                    cfg.value
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </td>
                <td className="px-3 py-2 text-xs text-muted-foreground">{cfg.source || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
