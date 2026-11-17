import type { K8sDetailProps } from "../registry";
import { selectorString, type LabelSelector } from "./selector";
import { useListed, type Listed } from "./useListed";

/** Pods matched by the workload's `spec.selector`; a workload without one matches nothing (not every Pod). */
export function useWorkloadPods({ assetId, namespace, object }: K8sDetailProps): Listed {
  const selector = selectorString((object.spec as { selector?: LabelSelector } | undefined)?.selector);
  return useListed(
    assetId,
    selector ? { group: "", version: "v1", resource: "pods", namespace, labelSelector: selector } : null
  );
}
