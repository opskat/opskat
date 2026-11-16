import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { AsyncState } from "@/components/common/AsyncState";
import { resourceTabId, useK8sStore } from "@/stores/k8sStore";
import { K8sLogsPanel } from "../../K8sLogsPanel";
import { podContainerNames } from "../pod/podInfo";
import type { K8sDetailProps } from "../registry";
import { useWorkloadPods } from "./useWorkloadPods";

/** The existing log panel on one Pod, with the container (and for a workload, the Pod) switchable. */
function PodLogs({
  props,
  pods,
  podName,
  container,
}: {
  props: K8sDetailProps;
  pods?: { name: string }[];
  podName: string;
  container: Record<string, unknown>;
}) {
  const { assetId, typeKey, namespace, name } = props;
  const tabId = resourceTabId(typeKey, namespace, name);
  const state = useK8sStore((s) => s.logTabs[assetId]?.[tabId]);
  const initLogTab = useK8sStore((s) => s.initLogTab);
  const updateLogTab = useK8sStore((s) => s.updateLogTab);
  useEffect(() => initLogTab(assetId, tabId), [assetId, tabId, initLogTab]);
  if (!state) return null;
  return (
    <div className="h-[32rem]">
      <K8sLogsPanel
        assetId={assetId}
        namespace={namespace}
        podName={podName}
        containers={podContainerNames(container).map((n) => ({ name: n }))}
        state={state}
        onStateChange={(update) => updateLogTab(assetId, tabId, update)}
        pods={pods}
        onSwitchPod={(pod) => updateLogTab(assetId, tabId, { currentPod: pod, logContainer: "" })}
      />
    </div>
  );
}

/** Logs sub-tab of a Pod: its own logs. */
export function PodLogsTab(props: K8sDetailProps) {
  return <PodLogs props={props} podName={props.name} container={props.object} />;
}

/** Logs sub-tab of a workload: the Pods its selector matches, one at a time. */
export function WorkloadLogsTab(props: K8sDetailProps) {
  const { t } = useTranslation();
  const { assetId, typeKey, namespace, name } = props;
  const pods = useWorkloadPods(props);
  const chosen = useK8sStore((s) => s.logTabs[assetId]?.[resourceTabId(typeKey, namespace, name)]?.currentPod);
  const pod = pods.items.find((p) => p.name === chosen) ?? pods.items[0];
  return (
    <AsyncState
      status={pods.status}
      error={pods.error}
      onRetry={pods.reload}
      empty={pods.status === "ready" && !pod ? t("asset.k8sNoPods") : undefined}
    >
      {pod && <PodLogs props={props} pods={pods.items} podName={pod.name} container={pod.object} />}
    </AsyncState>
  );
}
