import { TerminalSquare } from "lucide-react";
import { toast } from "sonner";
import { GetK8sResource } from "../../../../wailsjs/go/k8s/K8s";
import i18n from "@/i18n";
import { errorMessage } from "@/lib/errorMessage";
import { useK8sStore, type K8sResourceDetail, type K8sResourceRow } from "@/stores/k8sStore";
import { registerK8sAction, type K8sActionContext, type K8sActionTarget } from "../actions";
import { podContainers, podDefaultContainer } from "../detail/pod/podInfo";
import { startPodTerminal } from "./podTerminalRegistry";

const POD_TYPE_KEY = "/pods";

/**
 * The Pod's phase: that of a Pod object, or of a list row (the list reads it with the rows; the Status column
 * shows container reasons like CrashLoopBackOff instead). undefined when unknown.
 */
function podPhase(target: K8sActionTarget): string | undefined {
  if ("cells" in target && Array.isArray(target.cells)) return (target as K8sResourceRow).phase;
  return ((target as Record<string, unknown>).status as { phase?: string } | undefined)?.phase;
}

function notRunningReason(status: string | undefined): string | undefined {
  return status === undefined || status === "Running" ? undefined : i18n.t("asset.k8sTerminalNotRunning", { status });
}

/** Reads the Pod when the entry point only had a list row. */
async function loadPod(ctx: K8sActionContext): Promise<Record<string, unknown> | null> {
  if (ctx.object) return ctx.object;
  const detail = JSON.parse(
    await GetK8sResource({
      assetId: ctx.assetId,
      group: "",
      version: "v1",
      resource: "pods",
      namespace: ctx.namespace,
      name: ctx.name,
    } as never)
  ) as K8sResourceDetail;
  return detail.notFound ? null : detail.object;
}

/** Opens a new terminal tab on the Pod: the given container, else its default container. */
export async function openPodTerminal(ctx: K8sActionContext) {
  let pod: Record<string, unknown> | null;
  try {
    pod = await loadPod(ctx);
  } catch (e) {
    toast.error(errorMessage(e));
    return;
  }
  if (!pod) {
    toast.error(i18n.t("asset.k8sTerminalPodGone"));
    return;
  }
  const reason = notRunningReason(podPhase(pod) ?? "Unknown");
  if (reason) {
    toast.error(reason);
    return;
  }
  const containers = podContainers(pod)
    .filter((c) => !c.init)
    .map((c) => c.name);
  const container = ctx.container ?? podDefaultContainer(pod);
  const tabId = useK8sStore.getState().openTerminal(ctx.assetId, {
    namespace: ctx.namespace,
    pod: ctx.name,
    container,
    containers,
    shell: "auto",
  });
  startPodTerminal(ctx.assetId, tabId);
}

registerK8sAction({
  id: "terminal",
  labelKey: "asset.k8sTerminal",
  icon: TerminalSquare,
  primary: true,
  appliesTo: (typeKey) => typeKey === POD_TYPE_KEY,
  disabledReason: (target) => notRunningReason(podPhase(target)),
  run: openPodTerminal,
});
