import { useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Clock, RefreshCw, Search } from "lucide-react";
import { Button, Input, Tabs, TabsList, TabsTrigger, cn } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import { DEFAULT_LIST_VIEW, eventsKey, useK8sStore, type K8sClusterState } from "@/stores/k8sStore";
import { K8sEventsTable } from "./K8sEventsTable";

const EVENTS = "/events";

/** The sidebar 事件 entry: core events of the selected namespace, warnings by default. */
export function K8sEventsView({ assetId, cluster }: { assetId: number; cluster: K8sClusterState }) {
  const { t } = useTranslation();
  const namespace = cluster.namespace;
  const view = useK8sStore((s) => s.listViews[assetId]?.[EVENTS]) ?? DEFAULT_LIST_VIEW;
  const warningOnly = !view.showAllEvents;
  const entry = useK8sStore((s) => s.eventLists[assetId]?.[eventsKey(namespace, warningOnly)]);
  const loadEvents = useK8sStore((s) => s.loadEvents);
  const setListView = useK8sStore((s) => s.setListView);

  useEffect(() => {
    void loadEvents(assetId, namespace, warningOnly);
  }, [assetId, namespace, warningOnly, loadEvents]);

  const needle = view.filter.trim().toLowerCase();
  const shown = useMemo(
    () =>
      (entry?.data ?? []).filter(
        (e) =>
          !needle ||
          `${e.involvedObject.kind}/${e.involvedObject.name} ${e.reason} ${e.message}`.toLowerCase().includes(needle)
      ),
    [entry?.data, needle]
  );
  const reload = () => void loadEvents(assetId, namespace, warningOnly, { force: true });
  const status = entry?.status === "error" && !entry.data ? "error" : entry?.data ? "ready" : "loading";
  const reloading = entry?.status === "loading" && !!entry.data;

  return (
    <div className="flex h-full flex-col" data-testid="k8s-events-view">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        <Clock className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">{t("asset.k8sEvents")}</span>
        <span className="rounded-sm border px-1.5 font-mono text-[11px] text-muted-foreground">
          {namespace || t("asset.k8sAllNamespaces")}
        </span>
        <Tabs
          value={view.showAllEvents ? "all" : "warning"}
          onValueChange={(v) => setListView(assetId, EVENTS, { showAllEvents: v === "all" })}
          className="ml-2"
        >
          <TabsList className="h-7">
            <TabsTrigger value="warning" className="px-2 text-xs">
              {t("asset.k8sEventsWarning")}
            </TabsTrigger>
            <TabsTrigger value="all" className="px-2 text-xs">
              {t("asset.k8sEventsAll")}
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="ml-auto flex items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={view.filter}
              onChange={(e) => setListView(assetId, EVENTS, { filter: e.target.value })}
              placeholder={t("asset.k8sEventsFilterPlaceholder")}
              aria-label={t("asset.k8sEventsFilterPlaceholder")}
              className="h-7 w-56 pl-7 text-xs"
            />
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            onClick={reload}
            disabled={reloading}
            title={t("action.refresh")}
            aria-label={t("action.refresh")}
          >
            <RefreshCw className={cn("h-3.5 w-3.5", reloading && "animate-spin")} />
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-4">
        <AsyncState
          status={status}
          error={entry?.error}
          onRetry={reload}
          empty={
            entry?.data && shown.length === 0
              ? entry.data.length > 0
                ? t("asset.k8sNoMatch")
                : t(warningOnly ? "asset.k8sNoWarningEvents" : "asset.k8sNoEvents")
              : undefined
          }
          className="h-full"
        >
          <K8sEventsTable assetId={assetId} types={cluster.types.data?.types ?? []} events={shown} />
        </AsyncState>
      </div>
    </div>
  );
}
