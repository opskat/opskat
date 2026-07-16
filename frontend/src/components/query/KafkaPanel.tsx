import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  Activity,
  AlertCircle,
  FileJson,
  ListTree,
  Loader2,
  RefreshCw,
  Server,
  Settings,
  ShieldCheck,
  Users,
} from "lucide-react";
import { Button } from "@opskat/ui";
import { type KafkaTabState, type KafkaView, useKafkaStore } from "@/stores/kafkaStore";
import { ACLsView } from "./kafka/ACLsView";
import { BrokersView } from "./kafka/BrokersView";
import { ConsumerGroupsView } from "./kafka/ConsumerGroupsView";
import { KafkaConnectView } from "./kafka/KafkaConnectView";
import { OverviewView } from "./kafka/OverviewView";
import { SchemaRegistryView } from "./kafka/SchemaRegistryView";
import { TopicsView } from "./kafka/TopicsView";

interface KafkaPanelProps {
  tabId: string;
}

const VIEWS: { id: KafkaView; icon: typeof Activity; labelKey: string }[] = [
  { id: "overview", icon: Activity, labelKey: "query.kafkaOverview" },
  { id: "brokers", icon: Server, labelKey: "query.kafkaBrokers" },
  { id: "topics", icon: ListTree, labelKey: "query.kafkaTopics" },
  { id: "consumerGroups", icon: Users, labelKey: "query.kafkaConsumerGroups" },
  { id: "acls", icon: ShieldCheck, labelKey: "query.kafkaACLs" },
  { id: "schemas", icon: FileJson, labelKey: "query.kafkaSchemas" },
  { id: "connect", icon: Settings, labelKey: "query.kafkaConnect" },
];

export function KafkaPanel({ tabId }: KafkaPanelProps) {
  const { t } = useTranslation();
  const state = useKafkaStore((s) => s.states[tabId]);
  const ensureTab = useKafkaStore((s) => s.ensureTab);
  const setActiveView = useKafkaStore((s) => s.setActiveView);
  const refreshActiveView = useKafkaStore((s) => s.refreshActiveView);
  const loadOverview = useKafkaStore((s) => s.loadOverview);
  const loadBrokers = useKafkaStore((s) => s.loadBrokers);
  const loadTopics = useKafkaStore((s) => s.loadTopics);
  const loadConsumerGroups = useKafkaStore((s) => s.loadConsumerGroups);
  const loadACLs = useKafkaStore((s) => s.loadACLs);
  const loadSchemaSubjects = useKafkaStore((s) => s.loadSchemaSubjects);
  const loadConnectClusters = useKafkaStore((s) => s.loadConnectClusters);

  useEffect(() => {
    ensureTab(tabId);
    loadOverview(tabId);
    loadBrokers(tabId);
    loadTopics(tabId);
    loadConsumerGroups(tabId);
  }, [ensureTab, loadBrokers, loadConsumerGroups, loadOverview, loadTopics, tabId]);

  useEffect(() => {
    if (state?.activeView === "acls") {
      loadACLs(tabId);
    }
    if (state?.activeView === "schemas") {
      loadSchemaSubjects(tabId);
    }
    if (state?.activeView === "connect") {
      loadConnectClusters(tabId);
    }
  }, [loadACLs, loadConnectClusters, loadSchemaSubjects, state?.activeView, tabId]);

  const current = state || defaultPanelState();
  const busy =
    current.loadingOverview ||
    current.loadingBrokers ||
    current.loadingTopics ||
    current.loadingGroups ||
    current.loadingACLs ||
    current.loadingSchemaSubjects ||
    current.loadingConnectClusters ||
    current.loadingConnectors ||
    false;
  const activeLabel = t(VIEWS.find((view) => view.id === current.activeView)?.labelKey || "query.kafkaOverview");

  return (
    <div className="flex h-full w-full overflow-hidden">
      <aside className="w-56 shrink-0 border-r bg-muted/20">
        <div className="border-b px-3 py-2">
          <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Kafka</div>
        </div>
        <nav className="p-2">
          {VIEWS.map((view) => {
            const Icon = view.icon;
            const active = current.activeView === view.id;
            return (
              <button
                key={view.id}
                type="button"
                className={`flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-sm transition-colors ${
                  active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:bg-background/70"
                }`}
                onClick={() => setActiveView(tabId, view.id)}
              >
                <Icon className="h-4 w-4 shrink-0" />
                <span className="truncate">{t(view.labelKey)}</span>
              </button>
            );
          })}
        </nav>
      </aside>

      <main className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center justify-between border-b px-4">
          <div className="text-sm font-semibold">{activeLabel}</div>
          <div className="flex items-center gap-2">
            {current.error && (
              <span className="flex max-w-[480px] items-center gap-1 truncate text-xs text-destructive">
                <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                <span className="truncate">{current.error}</span>
              </span>
            )}
            <Button variant="outline" size="sm" className="h-7 gap-1.5" onClick={() => refreshActiveView(tabId)}>
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
              {t("query.refreshTree")}
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto">
          {current.activeView === "overview" && <OverviewView state={current} />}
          {current.activeView === "brokers" && <BrokersView tabId={tabId} state={current} />}
          {current.activeView === "topics" && <TopicsView tabId={tabId} state={current} />}
          {current.activeView === "consumerGroups" && <ConsumerGroupsView tabId={tabId} state={current} />}
          {current.activeView === "acls" && <ACLsView tabId={tabId} state={current} />}
          {current.activeView === "schemas" && <SchemaRegistryView tabId={tabId} state={current} />}
          {current.activeView === "connect" && <KafkaConnectView tabId={tabId} state={current} />}
        </div>
      </main>
    </div>
  );
}

function defaultPanelState(): KafkaTabState {
  return {
    activeView: "overview",
    brokers: [],
    topics: [],
    topicsTotal: 0,
    topicSearch: "",
    includeInternal: false,
    consumerGroups: [],
    acls: [],
    aclsTotal: 0,
    aclFilters: {
      resourceType: "any",
      resourceName: "",
      patternType: "any",
      principal: "",
      host: "",
      operation: "any",
      permission: "any",
    },
    schemaSubjects: [],
    connectClusters: [],
    connectors: [],
    messageBrowser: {
      partition: "",
      startMode: "newest",
      offset: "",
      timestampMillis: "",
      limit: 50,
      maxBytes: 4096,
      decodeMode: "text",
      maxWaitMillis: 1000,
    },
    produceMessage: {
      partition: "",
      key: "",
      value: "",
      headers: "",
      keyEncoding: "text",
      valueEncoding: "text",
    },
    loadingOverview: false,
    loadingBrokers: false,
    loadingBrokerConfig: false,
    loadingClusterConfigs: false,
    loadingTopics: false,
    loadingTopicDetail: false,
    loadingMessages: false,
    producingMessage: false,
    topicAdminLoading: false,
    groupAdminLoading: false,
    loadingGroups: false,
    loadingGroupDetail: false,
    loadingACLs: false,
    aclAdminLoading: false,
    loadingSchemaSubjects: false,
    loadingSchemaDetail: false,
    schemaAdminLoading: false,
    loadingConnectClusters: false,
    loadingConnectors: false,
    loadingConnectorDetail: false,
    connectAdminLoading: false,
    error: null,
  };
}
