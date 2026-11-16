import { useTranslation } from "react-i18next";
import { AsyncState } from "@/components/common/AsyncState";
import type { K8sEvent, Loadable } from "@/stores/k8sStore";
import { statusVariantToClass } from "../utils";

/** Events of one resource; the cluster returns them newest first and they are shown in that order. */
export function K8sEventsTab({ events, onRetry }: { events: Loadable<K8sEvent[]>; onRetry: () => void }) {
  const { t } = useTranslation();
  const list = events.data;
  return (
    <AsyncState
      status={events.status === "error" ? "error" : list ? "ready" : "loading"}
      error={events.error}
      onRetry={onRetry}
      empty={list && list.length === 0 ? t("asset.k8sNoEvents") : undefined}
      className="py-10"
    >
      {list && (
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b text-xs text-muted-foreground">
              <th className="py-2 pr-4 font-medium">{t("asset.k8sEventType")}</th>
              <th className="py-2 pr-4 font-medium">{t("asset.k8sEventReason")}</th>
              <th className="py-2 pr-4 font-medium">{t("asset.k8sEventMessage")}</th>
              <th className="py-2 pr-4 font-medium">{t("asset.k8sEventCount")}</th>
              <th className="py-2 font-medium">{t("asset.k8sEventLastSeen")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((e, i) => (
              <tr key={i} className="border-b align-top last:border-0">
                <td className="py-2 pr-4">
                  <span
                    className={`rounded-full px-1.5 py-0.5 text-xs ${statusVariantToClass(e.type === "Warning" ? "warning" : "info")}`}
                  >
                    {e.type}
                  </span>
                </td>
                <td className="py-2 pr-4 text-xs" data-testid="k8s-event-reason">
                  {e.reason}
                </td>
                <td className="select-text break-words py-2 pr-4 text-xs text-muted-foreground">{e.message}</td>
                <td className="py-2 pr-4 font-mono text-xs">{e.count}</td>
                <td className="whitespace-nowrap py-2 text-xs text-muted-foreground">{e.lastTimestamp}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </AsyncState>
  );
}
