import type { StatusVariant } from "../../utils";
import type { ContainerPort, ContainerResources } from "../workloads/summaryItems";
import { getContainerStateColor, getK8sStatusColor } from "../../utils";

interface ContainerState {
  running?: unknown;
  waiting?: { reason?: string };
  terminated?: { reason?: string; exitCode?: number; signal?: number };
}
interface ContainerStatus {
  name: string;
  ready?: boolean;
  started?: boolean;
  restartCount?: number;
  state?: ContainerState;
}
interface ContainerSpec {
  name: string;
  image?: string;
  ports?: ContainerPort[];
  resources?: ContainerResources;
  /** "Always" on an init container makes it a sidecar. */
  restartPolicy?: string;
}
interface PodShape {
  metadata?: { deletionTimestamp?: string; annotations?: Record<string, string> };
  spec?: { containers?: ContainerSpec[]; initContainers?: ContainerSpec[] };
  status?: {
    phase?: string;
    reason?: string;
    conditions?: { type: string; status: string }[];
    containerStatuses?: ContainerStatus[];
    initContainerStatuses?: ContainerStatus[];
  };
}

export interface PodContainerRow {
  name: string;
  image: string;
  state: string;
  stateVariant: StatusVariant;
  ready: boolean;
  restarts: number;
  ports?: ContainerPort[];
  resources?: ContainerResources;
  /** An init container: no terminal / logs entry points. */
  init: boolean;
}

function stateText(status: ContainerStatus | undefined): string {
  const state = status?.state;
  if (!state) return "Unknown";
  if (state.running) return "Running";
  if (state.waiting) return `Waiting: ${state.waiting.reason ?? ""}`;
  if (state.terminated) return `Terminated: ${state.terminated.reason ?? ""}`;
  return "Unknown";
}

/** The containers of a Pod object with the state of each (init containers last). */
export function podContainers(pod: Record<string, unknown>): PodContainerRow[] {
  const { spec, status } = pod as PodShape;
  const row = (c: ContainerSpec, statuses: ContainerStatus[], init: boolean): PodContainerRow => {
    const st = statuses.find((s) => s.name === c.name);
    const state = stateText(st);
    return {
      name: c.name,
      image: c.image ?? "",
      state,
      stateVariant: getContainerStateColor(state),
      ready: !!st?.ready,
      restarts: st?.restartCount ?? 0,
      ports: c.ports,
      resources: c.resources,
      init,
    };
  };
  const statuses = status?.containerStatuses ?? [];
  const initStatuses = status?.initContainerStatuses ?? [];
  return [
    ...(spec?.containers ?? []).map((c) => row(c, statuses, false)),
    ...(spec?.initContainers ?? []).map((c) => row(c, initStatuses, true)),
  ];
}

/** "ready/total" over the regular containers. */
export function podReady(pod: Record<string, unknown>): string {
  const rows = podContainers(pod).filter((c) => !c.init);
  return `${rows.filter((c) => c.ready).length}/${rows.length}`;
}

export function podRestarts(pod: Record<string, unknown>): number {
  return podContainers(pod)
    .filter((c) => !c.init)
    .reduce((n, c) => n + c.restarts, 0);
}

/** A terminated container's word when it has no reason: the signal, else the exit code. */
function exitText(t: NonNullable<ContainerState["terminated"]>): string {
  return t.signal ? `Signal:${t.signal}` : `ExitCode:${t.exitCode ?? 0}`;
}

/**
 * The status word `kubectl get pods` prints (its printPod, in the same order): a failing or pending init container
 * as `Init:<reason>` / `Init:<done>/<total>`, else the last regular container's waiting / terminated reason, else
 * the Pod's reason or phase; a deletion shows Terminating.
 */
export function podStatus(pod: Record<string, unknown>): { text: string; variant: StatusVariant } {
  const { status, metadata, spec } = pod as PodShape;
  let text = status?.reason || status?.phase || "Unknown";
  const conditionTrue = (type: string) => !!status?.conditions?.some((c) => c.type === type && c.status === "True");
  const initSpecs = spec?.initContainers ?? [];

  let initializing = false;
  const initStatuses = status?.initContainerStatuses ?? [];
  for (const [i, cs] of initStatuses.entries()) {
    const { terminated, waiting } = cs.state ?? {};
    if (terminated?.exitCode === 0) continue;
    // A started sidecar runs alongside the regular containers; it doesn't hold the Pod in Init.
    const sidecar = initSpecs.find((c) => c.name === cs.name)?.restartPolicy === "Always";
    if (sidecar && cs.started) continue;
    if (terminated) text = `Init:${terminated.reason || exitText(terminated)}`;
    else if (waiting?.reason && waiting.reason !== "PodInitializing") text = `Init:${waiting.reason}`;
    else text = `Init:${i}/${initSpecs.length}`;
    initializing = true;
    break;
  }

  if (!initializing || conditionTrue("Initialized")) {
    let hasRunning = false;
    for (const cs of [...(status?.containerStatuses ?? [])].reverse()) {
      const { terminated, waiting, running } = cs.state ?? {};
      if (waiting?.reason) text = waiting.reason;
      else if (terminated) text = terminated.reason || exitText(terminated);
      else if (cs.ready && running) hasRunning = true;
    }
    // A Pod whose finished container left another still running is not "Completed".
    if (text === "Completed" && hasRunning) text = conditionTrue("Ready") ? "Running" : "NotReady";
  }

  if (metadata?.deletionTimestamp) text = status?.reason === "NodeLost" ? "Unknown" : "Terminating";
  return { text, variant: getK8sStatusColor(text) };
}

/** Container names (regular, then init) of a Pod object, for pickers. */
export function podContainerNames(pod: Record<string, unknown>): string[] {
  return podContainers(pod).map((c) => c.name);
}

const DEFAULT_CONTAINER_ANNOTATION = "kubectl.kubernetes.io/default-container";

/** The container kubectl picks when none is named: the default-container annotation if it names one, else the first. */
export function podDefaultContainer(pod: Record<string, unknown>): string {
  const names = podContainers(pod)
    .filter((c) => !c.init)
    .map((c) => c.name);
  const annotated = (pod as PodShape).metadata?.annotations?.[DEFAULT_CONTAINER_ANNOTATION];
  return annotated && names.includes(annotated) ? annotated : names[0];
}
