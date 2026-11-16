import { RefreshCw, Scaling, Trash2 } from "lucide-react";
import { useK8sStore } from "@/stores/k8sStore";
import { registerK8sAction } from "../actions";

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

registerK8sAction({
  id: "delete",
  labelKey: "asset.k8sDeleteAction",
  icon: Trash2,
  destructive: true,
  appliesTo: (typeKey, _target, assetId) => supportsDelete(typeKey, assetId),
  run: (ctx) => useK8sStore.getState().openOp("delete", ctx),
});
