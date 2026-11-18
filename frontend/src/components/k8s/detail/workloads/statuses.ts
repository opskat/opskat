import { getK8sStatusColor } from "../../utils";
import type { DerivedStatus } from "../status";

type Obj = Record<string, unknown> | undefined;
interface Condition {
  type?: string;
  status?: string;
}

const conditionsOf = (object: Record<string, unknown>) =>
  ((object.status as { conditions?: Condition[] } | undefined)?.conditions ?? []) as Condition[];
const isTrue = (object: Record<string, unknown>, type: string) =>
  conditionsOf(object).some((c) => c.type === type && c.status === "True");
const pill = (text: string): DerivedStatus => ({ text, variant: getK8sStatusColor(text) });

/** A Job as `kubectl get jobs` prints its status: Failed / Complete / Suspended, else Running. */
export function jobStatus(object: Record<string, unknown>): DerivedStatus {
  if (isTrue(object, "Failed")) return pill("Failed");
  if (isTrue(object, "Complete")) return pill("Complete");
  if (isTrue(object, "Suspended")) return { text: "Suspended", variant: "warning" };
  return pill("Running");
}

/** A DaemonSet's ready vs desired scheduled Pods. */
export function daemonSetStatus(object: Record<string, unknown>): DerivedStatus | null {
  const status = object.status as Obj;
  const desired = typeof status?.desiredNumberScheduled === "number" ? status.desiredNumberScheduled : undefined;
  if (desired === undefined) return null;
  const ready = typeof status?.numberReady === "number" ? status.numberReady : 0;
  return { text: `${ready}/${desired}`, variant: ready >= desired ? "success" : "warning" };
}

/** Whether a CronJob is scheduling new Jobs. */
export function cronJobStatus(object: Record<string, unknown>): DerivedStatus {
  return (object.spec as Obj)?.suspend === true ? { text: "Suspended", variant: "warning" } : pill("Active");
}
