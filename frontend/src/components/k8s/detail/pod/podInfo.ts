import type { StatusVariant } from "../../utils";
import { getContainerStateColor, getK8sStatusColor } from "../../utils";

interface ContainerState {
  running?: unknown;
  waiting?: { reason?: string };
  terminated?: { reason?: string };
}
interface ContainerStatus {
  name: string;
  ready?: boolean;
  restartCount?: number;
  state?: ContainerState;
}
interface PodShape {
  metadata?: { deletionTimestamp?: string; annotations?: Record<string, string> };
  spec?: { containers?: { name: string; image?: string }[]; initContainers?: { name: string; image?: string }[] };
  status?: {
    phase?: string;
    reason?: string;
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
  const row = (c: { name: string; image?: string }, statuses: ContainerStatus[], init: boolean): PodContainerRow => {
    const st = statuses.find((s) => s.name === c.name);
    const state = stateText(st);
    return {
      name: c.name,
      image: c.image ?? "",
      state,
      stateVariant: getContainerStateColor(state),
      ready: !!st?.ready,
      restarts: st?.restartCount ?? 0,
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

/** The status word `kubectl get pods` would print: a waiting reason beats the phase, a deletion shows Terminating. */
export function podStatus(pod: Record<string, unknown>): { text: string; variant: StatusVariant } {
  const { status, metadata } = pod as PodShape;
  let text = status?.reason || status?.phase || "Unknown";
  for (const cs of status?.containerStatuses ?? []) {
    if (cs.state?.waiting?.reason) text = cs.state.waiting.reason;
  }
  if (metadata?.deletionTimestamp) text = "Terminating";
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
