import { useEffect } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";
import { AsyncState } from "@/components/common/AsyncState";
import { PanelSidebar, SidebarRefreshButton } from "@/components/common/PanelSidebar";
import { listTabId, OVERVIEW_TAB_ID, useK8sStore, type K8sClusterState } from "@/stores/k8sStore";
import { K8sNamespaceSelect } from "./cluster/K8sNamespaceSelect";
import { K8sTypeNav } from "./cluster/K8sTypeNav";
import { K8sInnerTabBar } from "./cluster/K8sInnerTabBar";
import { K8sResourceList } from "./cluster/K8sResourceList";
import { K8sEventsView } from "./cluster/K8sEventsView";
import { K8sOverview } from "./cluster/K8sOverview";
import { K8sResourceDetail } from "./detail/K8sResourceDetail";
import { PodTerminalTab } from "./terminal/PodTerminalTab";
import { K8sOpDialogs } from "./ops/K8sOpDialogs";
import { K8sYamlDialogs } from "./yaml/K8sYamlDialogs";
import "./detail/workloads/register";
import "./detail/resources/register";
import "./terminal/register";
import "./ops/register";
import "./yaml/register";

interface Props {
  asset: asset_entity.Asset;
}

/** The namespace configured on the asset ('' when none, meaning all namespaces). */
function configuredNamespace(asset: asset_entity.Asset): string {
  const cfg = JSON.parse(asset.Config || "{}") as { namespace?: string };
  return (cfg.namespace ?? "").trim();
}

/**
 * K8s cluster page. The page unmounts on every top-level tab switch, so everything it shows lives in
 * useK8sStore keyed by asset id; only reading the cluster version can block the whole page.
 */
export function K8sClusterPage({ asset }: Props) {
  const assetId = asset.ID;
  const cluster = useK8sStore((s) => s.clusters[assetId]);
  const initCluster = useK8sStore((s) => s.initCluster);
  const loadVersion = useK8sStore((s) => s.loadVersion);
  const refreshAll = useK8sStore((s) => s.refreshAll);
  const setNamespace = useK8sStore((s) => s.setNamespace);

  const defaultNamespace = configuredNamespace(asset);

  // Only seeds a cluster that has no state yet; a remount (or an edit to the asset) keeps the page as it was.
  useEffect(() => {
    initCluster(assetId, defaultNamespace);
  }, [assetId, defaultNamespace, initCluster]);

  const version = cluster?.version;
  if (!cluster || version?.status !== "ready" || !version.data) {
    return (
      <AsyncState
        status={version?.status === "error" ? "error" : "loading"}
        error={version?.error}
        onRetry={() => void loadVersion(assetId)}
        className="h-full"
      />
    );
  }

  return (
    <div className="flex h-full w-full">
      <PanelSidebar
        panelType="k8s"
        title={<span className="normal-case">{asset.Name}</span>}
        actions={<SidebarRefreshButton onClick={() => void refreshAll(assetId)} loading={cluster.refreshing} />}
        filter={
          <K8sNamespaceSelect
            value={cluster.namespace}
            namespaces={cluster.namespaces}
            onChange={(ns) => setNamespace(assetId, ns)}
          />
        }
      >
        <K8sTypeNav assetId={assetId} cluster={cluster} />
      </PanelSidebar>

      <div className="flex h-full min-w-0 flex-1 flex-col">
        <K8sInnerTabBar assetId={assetId} tabs={cluster.tabs} activeTabId={cluster.activeTabId} />
        <div className="min-h-0 flex-1 overflow-y-auto">
          <ActiveTab assetId={assetId} cluster={cluster} />
        </div>
      </div>
      <K8sYamlDialogs assetId={assetId} />
      <K8sOpDialogs assetId={assetId} />
    </div>
  );
}

function ActiveTab({ assetId, cluster }: { assetId: number; cluster: K8sClusterState }) {
  const id = cluster.activeTabId;
  if (id === OVERVIEW_TAB_ID && cluster.version.data) {
    return <K8sOverview assetId={assetId} cluster={cluster} version={cluster.version.data} />;
  }
  if (id === listTabId("/events")) {
    return <K8sEventsView key={id} assetId={assetId} cluster={cluster} />;
  }
  if (id.startsWith("list:")) {
    return <K8sResourceList key={id} assetId={assetId} cluster={cluster} typeKey={id.slice("list:".length)} />;
  }
  if (id.startsWith("res:")) {
    return <K8sResourceDetail key={id} assetId={assetId} cluster={cluster} tabId={id} />;
  }
  if (id.startsWith("term:")) {
    return <PodTerminalTab key={id} assetId={assetId} tabId={id} />;
  }
  return null;
}
