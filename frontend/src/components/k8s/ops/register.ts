import { CirclePause, CircleSlash, Play, RefreshCw, Scaling, Trash2 } from "lucide-react";
import { useK8sStore } from "@/stores/k8sStore";
import { registerK8sAction } from "../actions";
import { setCronJobSuspend } from "./cronOps";
import { isCronJobSuspended, isNodeUnschedulable, targetOf } from "./schedulingState";

const SCALABLE = new Set(["apps/deployments", "apps/statefulsets", "apps/replicasets"]);
const RESTARTABLE = new Set(["apps/deployments", "apps/statefulsets", "apps/daemonsets"]);
// A Node is cordoned / drained, not deleted from here.
const NOT_DELETABLE = new Set(["/nodes"]);

const supportsDelete = (typeKey: string, assetId?: number) =>
  assetId !== undefined &&
  !NOT_DELETABLE.has(typeKey) &&
  !!useK8sStore
    .getState()
    .clusters[assetId]?.types.data?.types.some(
      (type) => `${type.group}/${type.resource}` === typeKey && type.verbs.includes("delete")
    );

registerK8sAction({
  id: "scale",
  labelKey: "asset.k8sScaleAction",
  icon: Scaling,
  appliesTo: (typeKey) => SCALABLE.has(typeKey),
  run: (ctx) => useK8sStore.getState().openOp("scale", ctx),
});

registerK8sAction({
  id: "restart",
  labelKey: "asset.k8sRestartAction",
  icon: RefreshCw,
  appliesTo: (typeKey) => RESTARTABLE.has(typeKey),
  run: (ctx) => useK8sStore.getState().openOp("restart", ctx),
});

const CRONJOBS = "batch/cronjobs";
const NODES = "/nodes";

registerK8sAction({
  id: "cron-trigger",
  labelKey: "asset.k8sCronTriggerAction",
  icon: Play,
  primary: true,
  appliesTo: (typeKey) => typeKey === CRONJOBS,
  run: (ctx) => useK8sStore.getState().openOp("trigger", ctx),
});

registerK8sAction({
  id: "cron-suspend",
  labelKey: "asset.k8sCronSuspend",
  labelKeyFor: (target, columns) =>
    isCronJobSuspended(target, columns) ? "asset.k8sCronResume" : "asset.k8sCronSuspend",
  icon: CirclePause,
  appliesTo: (typeKey) => typeKey === CRONJOBS,
  run: async (ctx) => {
    const found = targetOf(ctx);
    await setCronJobSuspend(ctx, found ? !isCronJobSuspended(found.target, found.columns) : true);
  },
});

registerK8sAction({
  id: "node-cordon",
  labelKey: "asset.k8sCordonAction",
  labelKeyFor: (target) => (isNodeUnschedulable(target) ? "asset.k8sUncordonAction" : "asset.k8sCordonAction"),
  icon: CircleSlash,
  appliesTo: (typeKey) => typeKey === NODES,
  run: (ctx) => useK8sStore.getState().openOp("cordon", ctx),
});

registerK8sAction({
  id: "delete",
  labelKey: "asset.k8sDeleteAction",
  icon: Trash2,
  destructive: true,
  appliesTo: (typeKey, _target, assetId) => supportsDelete(typeKey, assetId),
  run: (ctx) => useK8sStore.getState().openOp("delete", ctx),
});
