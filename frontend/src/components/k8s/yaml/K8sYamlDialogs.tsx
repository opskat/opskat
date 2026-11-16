import { useK8sStore } from "@/stores/k8sStore";
import { K8sYamlApplyDialog } from "./K8sYamlApplyDialog";
import { K8sYamlEditDialog } from "./K8sYamlEditDialog";

/** Mounts the edit / apply YAML dialog the store has open on this cluster. */
export function K8sYamlDialogs({ assetId }: { assetId: number }) {
  const dialog = useK8sStore((s) => s.yamlDialogs[assetId]);
  if (!dialog) return null;
  if (dialog.mode === "apply") return <K8sYamlApplyDialog assetId={assetId} />;
  // Keyed so that opening another resource's editor starts from a fresh read.
  return (
    <K8sYamlEditDialog
      key={`${dialog.ctx.typeKey}:${dialog.ctx.namespace}:${dialog.ctx.name}`}
      assetId={assetId}
      ctx={dialog.ctx}
    />
  );
}
