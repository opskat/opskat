import { FilePenLine } from "lucide-react";
import { useK8sStore } from "@/stores/k8sStore";
import { registerK8sAction, type K8sActionContext } from "../actions";

// 编辑 YAML is offered for every type the cluster lets update (verbs come from discovery).
registerK8sAction({
  id: "edit-yaml",
  labelKey: "asset.k8sEditYamlAction",
  icon: FilePenLine,
  appliesTo: (typeKey, _target, assetId) =>
    assetId !== undefined &&
    !!useK8sStore
      .getState()
      .clusters[assetId]?.types.data?.types.some(
        (type) => `${type.group}/${type.resource}` === typeKey && type.verbs.includes("update")
      ),
  run: (ctx: K8sActionContext) => useK8sStore.getState().openYamlEdit(ctx),
});
