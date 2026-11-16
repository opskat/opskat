import { getK8sStatusColor, type StatusVariant } from "../utils";

export interface DerivedStatus {
  text: string;
  variant: StatusVariant;
}

type Obj = Record<string, unknown> | undefined;

/**
 * A status pill for any resource, from what its `status` carries: `phase`, else the Ready condition, else ready
 * vs desired replicas, else the Available condition. Resources with none of these (ConfigMap, CRDs…) get no pill.
 */
export function deriveStatus(object: Record<string, unknown>): DerivedStatus | null {
  const status = object.status as Obj;
  const spec = object.spec as Obj;
  if (!status) return null;

  if (typeof status.phase === "string" && status.phase) {
    return { text: status.phase, variant: getK8sStatusColor(status.phase) };
  }

  const conditions = Array.isArray(status.conditions) ? (status.conditions as Obj[]) : [];
  const conditionPill = (type: string): DerivedStatus | null => {
    const cond = conditions.find((c) => c?.type === type);
    if (!cond) return null;
    return cond.status === "True"
      ? { text: type, variant: "success" }
      : { text: `Not${type}`, variant: cond.status === "False" ? "error" : "warning" };
  };
  const ready = conditionPill("Ready");
  if (ready) return ready;

  const desired = typeof spec?.replicas === "number" ? spec.replicas : undefined;
  if (desired !== undefined) {
    const have = typeof status.readyReplicas === "number" ? status.readyReplicas : 0;
    return { text: `${have}/${desired}`, variant: have >= desired ? "success" : "warning" };
  }
  return conditionPill("Available");
}
