import { useK8sStore } from "@/stores/k8sStore";
import { K8sDeleteDialog, K8sRestartDialog } from "./K8sConfirmDialogs";
import { K8sScaleDialog } from "./K8sScaleDialog";

/** Mounts the scale / restart / delete dialog the store has open on this cluster. */
export function K8sOpDialogs({ assetId }: { assetId: number }) {
  const dialog = useK8sStore((s) => s.opDialogs[assetId]);
  if (!dialog) return null;
  const key = `${dialog.kind}:${dialog.ctx.typeKey}:${dialog.ctx.namespace}:${dialog.ctx.name}`;
  if (dialog.kind === "scale") return <K8sScaleDialog key={key} ctx={dialog.ctx} />;
  if (dialog.kind === "restart") return <K8sRestartDialog key={key} ctx={dialog.ctx} />;
  return <K8sDeleteDialog key={key} ctx={dialog.ctx} />;
}
