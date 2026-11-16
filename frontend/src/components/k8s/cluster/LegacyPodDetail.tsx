import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { ScrollText } from "lucide-react";
import { Button } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import { podDetailKey, useK8sStore } from "@/stores/k8sStore";
import { K8sSectionCard } from "../K8sSectionCard";
import { K8sResourceHeader } from "../K8sResourceHeader";
import { K8sMetadataGrid } from "../K8sMetadataGrid";
import { K8sTableSection } from "../K8sTableSection";
import { K8sConditionList } from "../K8sConditionList";
import { K8sTagList } from "../K8sTagList";
import { K8sCodeBlock } from "../K8sCodeBlock";
import { K8sLogsPanel } from "../K8sLogsPanel";
import { getContainerStateColor, getK8sStatusColor, statusVariantToClass } from "../utils";

/** Pod detail read through GetK8sPodDetail, kept until the generic resource detail replaces it. */
export function LegacyPodDetail({ assetId, namespace, pod }: { assetId: number; namespace: string; pod: string }) {
  const { t } = useTranslation();
  const entry = useK8sStore((s) => s.podDetails[assetId]?.[podDetailKey(namespace, pod)]);
  const loadPodDetail = useK8sStore((s) => s.loadPodDetail);
  const openPodLogs = useK8sStore((s) => s.openPodLogs);

  useEffect(() => {
    void loadPodDetail(assetId, namespace, pod);
  }, [assetId, namespace, pod, loadPodDetail]);

  const detail = entry?.data;
  return (
    <AsyncState
      status={entry?.status === "error" ? "error" : detail ? "ready" : "loading"}
      error={entry?.error}
      onRetry={() => void loadPodDetail(assetId, namespace, pod, { force: true })}
      className="h-full"
    >
      {detail && (
        <div className="mx-auto max-w-5xl space-y-4 p-4">
          <K8sSectionCard>
            <K8sResourceHeader
              name={detail.name}
              subtitle={`${detail.namespace} · ${detail.node_name}`}
              status={{ text: detail.status, variant: getK8sStatusColor(detail.status) }}
            />
            <K8sMetadataGrid
              items={[
                { label: t("asset.k8sPodIP"), value: detail.pod_ip || "-", mono: true },
                { label: t("asset.k8sPodHostIP"), value: detail.host_ip || "-", mono: true },
                { label: t("asset.k8sPodCreationTime"), value: detail.creation_time },
                { label: t("asset.k8sPodReady"), value: detail.ready, mono: true },
                { label: t("asset.k8sPodQosClass"), value: detail.qos_class },
              ]}
            />
          </K8sSectionCard>

          <K8sTableSection
            title={t("asset.k8sPodContainers")}
            columns={[
              { key: "name", label: t("asset.k8sPodName") },
              { key: "image", label: "Image" },
              { key: "state", label: t("asset.k8sPodStatus") },
              { key: "ready", label: t("asset.k8sPodReady") },
              { key: "restarts", label: t("asset.k8sPodRestarts") },
            ]}
            data={detail.containers}
            emptyText={t("asset.k8sNoEvents")}
            renderRow={(c) => (
              <tr key={c.name} className="border-b last:border-0">
                <td className="py-2 pr-4 font-mono text-sm">{c.name}</td>
                <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{c.image}</td>
                <td className="py-2 pr-4">
                  <span
                    className={`text-xs px-1.5 py-0.5 rounded-full ${statusVariantToClass(getContainerStateColor(c.state))}`}
                  >
                    {c.state}
                  </span>
                </td>
                <td className="py-2 pr-4">
                  <span className={c.ready ? "text-success" : "text-destructive"}>{c.ready ? "✓" : "✗"}</span>
                </td>
                <td className="py-2 font-mono text-sm">{c.restart_count}</td>
              </tr>
            )}
          />

          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                openPodLogs(
                  assetId,
                  detail.namespace,
                  detail.name,
                  detail.containers[0]?.name ?? "",
                  `${t("asset.k8sPodLogs")}: ${detail.name}`
                )
              }
            >
              <ScrollText className="h-3 w-3" />
              {t("asset.k8sPodLogs")}
            </Button>
          </div>

          <K8sTableSection
            title={t("asset.k8sPodEvents")}
            columns={[
              { key: "type", label: "Type" },
              { key: "reason", label: "Reason" },
              { key: "message", label: "Message" },
              { key: "count", label: "Count" },
              { key: "last_time", label: "Last Seen" },
            ]}
            data={detail.events}
            emptyText={t("asset.k8sNoEvents")}
            renderRow={(e, i) => (
              <tr key={i} className="border-b last:border-0">
                <td className="py-2 pr-4">
                  <span
                    className={`text-xs px-1.5 py-0.5 rounded-full ${statusVariantToClass(e.type === "Warning" ? "warning" : "info")}`}
                  >
                    {e.type}
                  </span>
                </td>
                <td className="py-2 pr-4 text-xs">{e.reason}</td>
                <td className="py-2 pr-4 text-xs text-muted-foreground max-w-xs truncate">{e.message}</td>
                <td className="py-2 pr-4 font-mono text-xs">{e.count}</td>
                <td className="py-2 text-xs text-muted-foreground">{e.last_time}</td>
              </tr>
            )}
          />

          <K8sConditionList conditions={detail.conditions} title={t("asset.k8sPodConditions")} defaultCollapsed />
          <K8sTagList tags={detail.labels} title={t("asset.k8sPodLabels")} defaultCollapsed />
          <K8sCodeBlock code={detail.yaml} title={t("asset.k8sPodYAML")} defaultCollapsed />
        </div>
      )}
    </AsyncState>
  );
}

/** Log tab of one pod, using the pod detail's containers. */
export function LegacyPodLogs({
  assetId,
  namespace,
  pod,
  tabId,
}: {
  assetId: number;
  namespace: string;
  pod: string;
  tabId: string;
}) {
  const state = useK8sStore((s) => s.logTabs[assetId]?.[tabId]);
  const detail = useK8sStore((s) => s.podDetails[assetId]?.[podDetailKey(namespace, pod)]?.data);
  const updateLogTab = useK8sStore((s) => s.updateLogTab);
  if (!state || !detail) return null;
  return (
    <div className="flex h-full flex-col p-4">
      <K8sLogsPanel
        assetId={assetId}
        containers={detail.containers}
        namespace={namespace}
        podName={pod}
        state={state}
        onStateChange={(update) => updateLogTab(assetId, tabId, update)}
      />
    </div>
  );
}
