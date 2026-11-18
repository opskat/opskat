import { FilePenLine } from "lucide-react";
import { k8sTypeOf, useK8sStore } from "@/stores/k8sStore";
import { registerK8sAction, type K8sActionContext } from "../actions";

// 编辑 YAML is offered for every type the cluster lets update (verbs come from discovery).
registerK8sAction({
  id: "edit-yaml",
  labelKey: "asset.k8sEditYamlAction",
  icon: FilePenLine,
  appliesTo: (typeKey, _target, assetId) =>
    assetId !== undefined && !!k8sTypeOf(useK8sStore.getState().clusters[assetId], typeKey)?.verbs.includes("update"),
  run: (ctx: K8sActionContext) => useK8sStore.getState().openYamlEdit(ctx),
});
