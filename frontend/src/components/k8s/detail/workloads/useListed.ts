import { useCallback, useEffect, useRef, useState } from "react";
import { errorMessage } from "@/lib/errorMessage";
import { useK8sStore } from "@/stores/k8sStore";
import { ListK8sResources } from "../../../../../wailsjs/go/k8s/K8s";

export interface ListedObject {
  name: string;
  object: Record<string, unknown>;
}

export interface Listed {
  status: "loading" | "ready" | "error";
  items: ListedObject[];
  error: string | null;
  reload: () => void;
}

interface Request {
  group: string;
  version: string;
  resource: string;
  namespace: string;
  labelSelector: string;
}

/**
 * Objects of one resource type with their full bodies, read when mounted and re-read with the page's refresh.
 * `request` null = nothing to ask for (an empty list). `refreshKey` identifies the view to the refresh button.
 */
export function useListed(assetId: number, refreshKey: string, request: Request | null): Listed {
  const [state, setState] = useState<Omit<Listed, "reload">>({ status: "loading", items: [], error: null });
  const registerRefresher = useK8sStore((s) => s.registerRefresher);
  const group = request?.group;
  const version = request?.version;
  const resource = request?.resource;
  const namespace = request?.namespace;
  const labelSelector = request?.labelSelector;
  const seq = useRef(0);

  const read = useCallback(async () => {
    if (group === undefined || version === undefined || resource === undefined || namespace === undefined) {
      setState({ status: "ready", items: [], error: null });
      return;
    }
    const mine = ++seq.current;
    try {
      const result = await ListK8sResources({
        assetId,
        group,
        version,
        resource,
        namespace,
        labelSelector: labelSelector ?? "",
        fieldSelector: "",
        includeObject: true,
      });
      if (mine !== seq.current) return;
      const rows = (JSON.parse(result) as { rows: { name: string; object?: Record<string, unknown> }[] }).rows;
      setState({
        status: "ready",
        items: rows.flatMap((r) => (r.object ? [{ name: r.name, object: r.object }] : [])),
        error: null,
      });
    } catch (e) {
      if (mine !== seq.current) return;
      setState((prev) => ({ ...prev, status: "error", error: errorMessage(e) }));
    }
  }, [assetId, group, version, resource, namespace, labelSelector]);

  useEffect(() => {
    const counter = seq;
    void read();
    return () => {
      counter.current++; // a read still in flight must not land after unmount / a new request
    };
  }, [read]);
  useEffect(() => registerRefresher(assetId, refreshKey, read), [registerRefresher, assetId, refreshKey, read]);

  return { ...state, reload: () => void read() };
}
