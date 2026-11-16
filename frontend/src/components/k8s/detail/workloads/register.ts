import { ScrollText } from "lucide-react";
import { registerK8sAction } from "../../actions";
import { PodOverview } from "../pod/PodOverview";
import { registerDetailOverview, registerDetailSubTab } from "../registry";
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

registerDetailOverview("Pod", PodOverview);
registerDetailSubTab("Pod", { id: "logs", labelKey: "asset.k8sLogs", Component: PodLogsTab });

for (const [kind, overview] of [
  ["Deployment", DeploymentOverview],
  ["StatefulSet", StatefulSetOverview],
  ["DaemonSet", DaemonSetOverview],
  ["ReplicaSet", ReplicaSetOverview],
  ["Job", JobOverview],
] as const) {
  registerDetailOverview(kind, overview);
  registerDetailSubTab(kind, PODS_TAB);
  registerDetailSubTab(kind, LOGS_TAB);
}
registerDetailOverview("CronJob", CronJobOverview);

registerK8sAction({
  id: "logs",
  labelKey: "asset.k8sLogs",
  icon: ScrollText,
  appliesTo: (typeKey) => LOGGABLE.has(typeKey),
  run: (ctx) => openResourceSubTab(ctx, "logs"),
});
