import { useK8sStore } from "@/stores/k8sStore";
import { useListed } from "./useListed";

const HPA_TYPE_KEY = "autoscaling/horizontalpodautoscalers";

export interface RelatedHpa {
  name: string;
  min: number;
  max: number;
}

/**
 * The HPA of a workload (same namespace, `scaleTargetRef` pointing at it), read at the version the cluster serves
 * (autoscaling/v1 on clusters before 1.23); `hpa` null when none (or the cluster has no HPAs), `failed` when the
 * lookup errored.
 */
export function useHpaOf(assetId: number, namespace: string, kind: string, name: string) {
  const version = useK8sStore(
    (s) => s.clusters[assetId]?.types.data?.types.find((t) => `${t.group}/${t.resource}` === HPA_TYPE_KEY)?.version
  );
  const listed = useListed(
    assetId,
    version
      ? { group: "autoscaling", version, resource: "horizontalpodautoscalers", namespace, labelSelector: "" }
      : null
  );
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
