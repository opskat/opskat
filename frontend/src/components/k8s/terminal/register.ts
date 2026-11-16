import { TerminalSquare } from "lucide-react";
import { toast } from "sonner";
import { GetK8sResource } from "../../../../wailsjs/go/k8s/K8s";
import i18n from "@/i18n";
import { errorMessage } from "@/lib/errorMessage";
import { useK8sStore, type K8sResourceDetail, type K8sTableColumn } from "@/stores/k8sStore";
import { registerK8sAction, type K8sActionContext, type K8sActionTarget } from "../actions";
import { cellText } from "../cluster/listModel";
import { podContainers, podDefaultContainer } from "../detail/pod/podInfo";
import { isStatusColumn } from "../utils";
import { startPodTerminal } from "./podTerminalRegistry";

const POD_TYPE_KEY = "/pods";

/**
 * Whether the Pod is running: the phase of a Pod object; for a list row (no object) its status column, which
 * shows the phase unless a container or deletion reason overrides it. undefined when unknown.
 */
function podRunStatus(target: K8sActionTarget, columns?: K8sTableColumn[]): string | undefined {
  if ("cells" in target && Array.isArray(target.cells)) {
    const idx = columns?.findIndex(isStatusColumn) ?? -1;
    return idx >= 0 ? cellText(target.cells[idx]) || undefined : undefined;
  }
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
  const reason = notRunningReason(podRunStatus(pod) ?? "Unknown");
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
  disabledReason: (target, columns) => notRunningReason(podRunStatus(target, columns)),
  run: openPodTerminal,
});
