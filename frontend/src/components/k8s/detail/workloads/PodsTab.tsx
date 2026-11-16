import { useTranslation } from "react-i18next";
import { ScrollText } from "lucide-react";
import { Button } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import { useK8sStore, resourceTabId } from "@/stores/k8sStore";
import { getK8sActions } from "../../actions";
import { K8sSectionCard } from "../../K8sSectionCard";
import { statusVariantToClass } from "../../utils";
import { podReady, podRestarts, podStatus } from "../pod/podInfo";
import type { K8sDetailProps } from "../registry";
import { openResourceSubTab } from "./openSubTab";
import { useWorkloadPods } from "./useWorkloadPods";

const POD_TYPE_KEY = "/pods";

/** Pods matched by the workload's selector; the name opens the Pod, each row has logs and (when available) terminal entries. */
export function PodsTab(props: K8sDetailProps) {
  const { t } = useTranslation();
  const { assetId, namespace } = props;
  const openTab = useK8sStore((s) => s.openTab);
  const pods = useWorkloadPods(props);

  return (
    <AsyncState
      status={pods.status}
      error={pods.error}
      onRetry={pods.reload}
      empty={pods.status === "ready" && pods.items.length === 0 ? t("asset.k8sNoPods") : undefined}
    >
      <K8sSectionCard>
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b">
              {[
                t("asset.k8sPodName"),
                t("asset.k8sPodReady"),
                t("asset.k8sPodStatus"),
                t("asset.k8sPodRestarts"),
                t("asset.k8sPodNode"),
                "",
              ].map((h, i) => (
                <th key={i} className="py-2 pr-4 text-left text-xs font-medium text-muted-foreground">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {pods.items.map((p) => {
              const status = podStatus(p.object);
              const ctx = { assetId, typeKey: POD_TYPE_KEY, namespace, name: p.name, object: p.object };
              const terminal = getK8sActions(POD_TYPE_KEY, p.object).find((a) => a.id === "terminal");
              const terminalDisabled = terminal?.disabledReason?.(p.object);
              return (
                <tr key={p.name} className="border-b last:border-0 hover:bg-muted/30">
                  <td className="py-1.5 pr-4 font-mono">
                    <button
                      type="button"
                      role="link"
                      className="font-medium text-primary hover:underline"
                      onClick={() =>
                        openTab(assetId, { id: resourceTabId(POD_TYPE_KEY, namespace, p.name), label: p.name })
                      }
                    >
                      {p.name}
                    </button>
                  </td>
                  <td className="py-1.5 pr-4 font-mono text-xs">{podReady(p.object)}</td>
                  <td className="py-1.5 pr-4">
                    <span className={`rounded-full px-1.5 py-0.5 text-xs ${statusVariantToClass(status.variant)}`}>
                      {status.text}
                    </span>
                  </td>
                  <td className="py-1.5 pr-4 font-mono text-xs">{podRestarts(p.object)}</td>
                  <td className="py-1.5 pr-4 font-mono text-xs">
                    {(p.object.spec as { nodeName?: string } | undefined)?.nodeName ?? "-"}
                  </td>
                  <td className="py-1 text-right">
                    {terminal && (
                      // The wrapper carries the hover text: a disabled button gets no hover.
                      <span title={terminalDisabled ?? t(terminal.labelKey)}>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-6 w-6"
                          aria-label={t(terminal.labelKey)}
                          disabled={!!terminalDisabled}
                          onClick={() => void terminal.run(ctx)}
                        >
                          <terminal.icon className="h-3.5 w-3.5" />
                        </Button>
                      </span>
                    )}
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-6 w-6"
                      title={t("asset.k8sLogs")}
                      aria-label={t("asset.k8sLogs")}
                      onClick={() => openResourceSubTab(ctx, "logs")}
                    >
                      <ScrollText className="h-3.5 w-3.5" />
                    </Button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </K8sSectionCard>
    </AsyncState>
  );
}
