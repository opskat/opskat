import { ScrollText } from "lucide-react";
import { registerK8sAction } from "../../actions";
import { PodOverview } from "../pod/PodOverview";
import { podStatus } from "../pod/podInfo";
import { registerDetailOverview, registerDetailStatus, registerDetailSubTab } from "../registry";
import { PodLogsTab, WorkloadLogsTab } from "./LogsTab";
import { openResourceSubTab } from "./openSubTab";
import {
  CronJobOverview,
  DaemonSetOverview,
  DeploymentOverview,
  JobOverview,
  ReplicaSetOverview,
  StatefulSetOverview,
} from "./overviews";
import { PodsTab } from "./PodsTab";
import { cronJobStatus, daemonSetStatus, jobStatus } from "./statuses";

/** Type keys (`<group>/<resource>`) whose detail has a Logs sub-tab and a 「日志」 action. */
const LOGGABLE = new Set([
  "/pods",
  "apps/deployments",
  "apps/statefulsets",
  "apps/daemonsets",
  "apps/replicasets",
  "batch/jobs",
]);

const PODS_TAB = { id: "pods", labelKey: "asset.k8sPodsTab", Component: PodsTab };
const LOGS_TAB = { id: "logs", labelKey: "asset.k8sLogs", Component: WorkloadLogsTab };

registerDetailOverview("/pods", PodOverview);
registerDetailSubTab("/pods", { id: "logs", labelKey: "asset.k8sLogs", Component: PodLogsTab });
registerDetailStatus("/pods", podStatus);

for (const [typeKey, overview] of [
  ["apps/deployments", DeploymentOverview],
  ["apps/statefulsets", StatefulSetOverview],
  ["apps/daemonsets", DaemonSetOverview],
  ["apps/replicasets", ReplicaSetOverview],
  ["batch/jobs", JobOverview],
] as const) {
  registerDetailOverview(typeKey, overview);
  registerDetailSubTab(typeKey, PODS_TAB);
  registerDetailSubTab(typeKey, LOGS_TAB);
}
registerDetailOverview("batch/cronjobs", CronJobOverview);
registerDetailStatus("batch/jobs", jobStatus);
registerDetailStatus("apps/daemonsets", daemonSetStatus);
registerDetailStatus("batch/cronjobs", cronJobStatus);

registerK8sAction({
  id: "logs",
  labelKey: "asset.k8sLogs",
  icon: ScrollText,
  appliesTo: (typeKey) => LOGGABLE.has(typeKey),
  run: (ctx) => openResourceSubTab(ctx, "logs"),
});
