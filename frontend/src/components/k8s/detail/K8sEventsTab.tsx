import { useTranslation } from "react-i18next";
import type { K8sEvent, K8sResourceType, Loadable } from "@/stores/k8sStore";
import { K8sAsyncState } from "../K8sAsyncState";
import { K8sEventsTable } from "../cluster/K8sEventsTable";

/** Events of one resource, newest first as the cluster returns them; a failed re-read keeps those already shown. */
export function K8sEventsTab({
  assetId,
  types,
  events,
  onRetry,
}: {
  assetId: number;
  types: K8sResourceType[];
  events: Loadable<K8sEvent[]>;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const list = events.data;
  return (
    <K8sAsyncState
      status={events.status === "error" ? "error" : list ? "ready" : "loading"}
      hasData={!!list}
      error={events.error}
      onRetry={onRetry}
      empty={list && list.length === 0 ? t("asset.k8sNoEvents") : undefined}
      className="py-10"
    >
      {list && <K8sEventsTable assetId={assetId} types={types} events={list} hideObject />}
    </K8sAsyncState>
  );
}
