import { useCallback, useEffect } from "react";
import { objectsKey, useK8sStore, type K8sListedObject, type K8sObjectsRequest } from "@/stores/k8sStore";

export type ListedObject = K8sListedObject;

export interface Listed {
  status: "loading" | "ready" | "error";
  items: ListedObject[];
  error: string | null;
  /** Some items were read before (a failed re-read keeps them). */
  hasData: boolean;
  reload: () => void;
}

const NONE: ListedObject[] = [];

/**
 * Objects of one resource type with their full bodies, cached in the K8s store: read on first use, then only by
 * the page's refresh or `reload`. `request` null = nothing to ask for (an empty list).
 */
export function useListed(assetId: number, request: K8sObjectsRequest | null): Listed {
  const key = request ? objectsKey(request) : null;
  const entry = useK8sStore((s) => (key ? s.objectLists[assetId]?.[key] : undefined));
  const loadObjects = useK8sStore((s) => s.loadObjects);
  // The caller rebuilds the request object on every render, so it is passed on by its fields.
  const { group = "", version = "", resource = "", namespace = "", labelSelector = "" } = request ?? {};
  const load = useCallback(
    (force: boolean) => {
      if (key) void loadObjects(assetId, { group, version, resource, namespace, labelSelector }, { force });
    },
    [key, assetId, group, version, resource, namespace, labelSelector, loadObjects]
  );

  useEffect(() => load(false), [load]);

  if (!key) return { status: "ready", items: NONE, error: null, hasData: false, reload: () => {} };
  return {
    status: entry?.status ?? "loading",
    items: entry?.data ?? NONE,
    error: entry?.error ?? null,
    hasData: entry?.data !== undefined,
    reload: () => load(true),
  };
}
