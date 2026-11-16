import { useListed } from "./useListed";

export interface RelatedHpa {
  name: string;
  min: number;
  max: number;
}

/** The HPA of a workload (same namespace, `scaleTargetRef` pointing at it); `hpa` null when none, `failed` when the lookup errored. */
export function useHpaOf(assetId: number, namespace: string, kind: string, name: string) {
  const listed = useListed(assetId, `hpa:${namespace}:${kind}:${name}`, {
    group: "autoscaling",
    version: "v2",
    resource: "horizontalpodautoscalers",
    namespace,
    labelSelector: "",
  });
  const found = listed.items.find((h) => {
    const ref = (h.object.spec as { scaleTargetRef?: { kind?: string; name?: string } } | undefined)?.scaleTargetRef;
    return ref?.kind === kind && ref?.name === name;
  });
  const spec = (found?.object.spec ?? {}) as { minReplicas?: number; maxReplicas?: number };
  const hpa: RelatedHpa | null = found
    ? { name: found.name, min: spec.minReplicas ?? 1, max: spec.maxReplicas ?? 0 }
    : null;
  return { failed: listed.status === "error", hpa };
}
