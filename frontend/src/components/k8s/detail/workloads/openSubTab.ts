import { resourceTabId, useK8sStore } from "@/stores/k8sStore";
import type { K8sActionContext } from "../../actions";

/** Opens a resource's detail tab on one of its sub-tabs; for 'logs', `container` preselects the container. */
export function openResourceSubTab(
  { assetId, typeKey, namespace, name }: Pick<K8sActionContext, "assetId" | "typeKey" | "namespace" | "name">,
  subTab: string,
  container?: string
) {
  const store = useK8sStore.getState();
  const id = resourceTabId(typeKey, namespace, name);
  store.openTab(assetId, { id, label: name });
  if (subTab === "logs" && container) {
    store.initLogTab(assetId, id);
    store.updateLogTab(assetId, id, { logContainer: container });
  }
  store.setDetailSubTab(assetId, id, subTab);
}
