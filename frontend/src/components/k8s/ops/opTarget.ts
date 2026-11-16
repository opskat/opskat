import { toast } from "sonner";
import { errorMessage } from "@/lib/errorMessage";
import { notifySuccess } from "@/lib/notify";
import { useK8sStore, type K8sResourceType } from "@/stores/k8sStore";
import type { K8sActionContext } from "../actions";

export function typeOf(assetId: number, typeKey: string): K8sResourceType | undefined {
  return useK8sStore
    .getState()
    .clusters[assetId]?.types.data?.types.find((t) => `${t.group}/${t.resource}` === typeKey);
}

/** The IPC reference of the resource an action was started from. */
export function refOf(ctx: K8sActionContext) {
  const type = typeOf(ctx.assetId, ctx.typeKey);
  const [group, resource] = [type?.group ?? "", type?.resource ?? ctx.typeKey.slice(ctx.typeKey.indexOf("/") + 1)];
  return {
    assetId: ctx.assetId,
    group,
    version: type?.version ?? "v1",
    resource,
    namespace: ctx.namespace,
    name: ctx.name,
  };
}

interface OwnerRef {
  kind: string;
  name: string;
  controller?: boolean;
}

/** `Kind/name` of the controller that owns the resource, read from the object the action carries or the cached list row. */
export function controllerOf(ctx: K8sActionContext): string | null {
  const state = useK8sStore.getState();
  let owners = (ctx.object?.metadata as { ownerReferences?: OwnerRef[] } | undefined)?.ownerReferences;
  if (!owners) {
    for (const [key, entry] of Object.entries(state.lists[ctx.assetId] ?? {})) {
      if (!key.startsWith(`${ctx.typeKey}|`)) continue;
      const row = entry.rows.find((r) => r.name === ctx.name && r.namespace === ctx.namespace);
      if (row) owners = row.ownerReferences;
    }
  }
  const owner = owners?.find((o) => o.controller);
  return owner ? `${owner.kind}/${owner.name}` : null;
}

/**
 * Runs one write against the cluster. Success: a toast, then the follow-up (close tabs, refresh). Failure: the
 * cluster's reason verbatim in a toast and nothing else changes. Returns whether it succeeded.
 */
export async function runK8sOp(call: () => Promise<unknown>, successMessage: string, after: () => void) {
  try {
    await call();
  } catch (e) {
    toast.error(errorMessage(e));
    return false;
  }
  notifySuccess(successMessage);
  after();
  return true;
}
