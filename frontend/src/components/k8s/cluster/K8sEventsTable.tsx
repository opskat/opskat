import { useTranslation } from "react-i18next";
import { resourceTabId, typeKeyOf, useK8sStore, type K8sEvent, type K8sResourceType } from "@/stores/k8sStore";
import { statusVariantToClass } from "../utils";

/** The list type an event's involved object belongs to (matched on Kind and API group); undefined when the cluster doesn't list it. */
function typeOfObject(types: K8sResourceType[], obj: K8sEvent["involvedObject"]) {
  const slash = obj.apiVersion.lastIndexOf("/");
  const group = slash < 0 ? "" : obj.apiVersion.slice(0, slash);
  return types.find((t) => t.kind === obj.kind && t.group === group);
}

interface Props {
  assetId: number;
  types: K8sResourceType[];
  events: K8sEvent[];
  /** Overview preview: no count column. */
  compact?: boolean;
  /** The events of one resource: no object column. */
  hideObject?: boolean;
}

/** Events as a table: type, reason, object (a link when its type is listed), message, count, last seen. */
export function K8sEventsTable({ assetId, types, events, compact, hideObject }: Props) {
  const { t } = useTranslation();
  const openTab = useK8sStore((s) => s.openTab);
  return (
    <table className="w-full text-left text-sm">
      <thead>
        <tr className="border-b text-xs text-muted-foreground">
          <th className="py-2 pr-4 font-medium">{t("asset.k8sEventType")}</th>
          <th className="py-2 pr-4 font-medium">{t("asset.k8sEventReason")}</th>
          {!hideObject && <th className="py-2 pr-4 font-medium">{t("asset.k8sEventObject")}</th>}
          <th className="py-2 pr-4 font-medium">{t("asset.k8sEventMessage")}</th>
          {!compact && <th className="py-2 pr-4 font-medium">{t("asset.k8sEventCount")}</th>}
          <th className="py-2 font-medium">{t("asset.k8sEventLastSeen")}</th>
        </tr>
      </thead>
      <tbody>
        {events.map((e, i) => {
          const obj = e.involvedObject;
          const target = typeOfObject(types, obj);
          const label = `${obj.kind}/${obj.name}`;
          return (
            <tr key={i} className="border-b align-top last:border-0" data-testid="k8s-event-row">
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
              {!hideObject && (
                <td className="py-2 pr-4 font-mono text-xs">
                  {target ? (
                    <button
                      className="text-primary hover:underline"
                      data-testid="k8s-event-object"
                      onClick={() =>
                        openTab(assetId, {
                          id: resourceTabId(typeKeyOf(target), obj.namespace, obj.name),
                          label: obj.name,
                        })
                      }
                    >
                      {label}
                    </button>
                  ) : (
                    <span data-testid="k8s-event-object">{label}</span>
                  )}
                </td>
              )}
              <td className="select-text break-words py-2 pr-4 text-xs text-muted-foreground">{e.message}</td>
              {!compact && <td className="py-2 pr-4 font-mono text-xs">{e.count}</td>}
              <td className="whitespace-nowrap py-2 text-xs text-muted-foreground">{e.lastTimestamp}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
