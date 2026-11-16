import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Server } from "lucide-react";
import { AsyncState } from "@/components/common/AsyncState";
import { listKey, useK8sStore, type K8sClusterVersion } from "@/stores/k8sStore";
import { K8sSectionCard } from "../K8sSectionCard";
import { K8sResourceTable } from "./K8sResourceTable";

const NODES = "/nodes";

/** Cluster overview: version / platform and the node list. */
export function K8sOverview({ assetId, version }: { assetId: number; version: K8sClusterVersion }) {
  const { t } = useTranslation();
  const nodes = useK8sStore((s) => s.lists[assetId]?.[listKey(NODES, "")]);
  const loadList = useK8sStore((s) => s.loadList);

  useEffect(() => {
    void loadList(assetId, NODES, "");
  }, [assetId, loadList]);

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-4">
      <K8sSectionCard>
        <p className="text-xs text-muted-foreground">Kubernetes</p>
        <p className="mt-1 font-mono text-lg font-semibold">{version.version}</p>
        <p className="mt-0.5 text-xs text-muted-foreground">{version.platform}</p>
      </K8sSectionCard>
      <K8sSectionCard title={t("asset.k8sNodes")} icon={Server}>
        <AsyncState
          status={nodes?.status ?? "loading"}
          error={nodes?.error}
          onRetry={() => void loadList(assetId, NODES, "", { force: true })}
          empty={nodes?.status === "ready" && nodes.rows.length === 0 ? t("asset.k8sNoNodes") : undefined}
        >
          {nodes && <K8sResourceTable columns={nodes.columns} rows={nodes.rows} />}
        </AsyncState>
      </K8sSectionCard>
    </div>
  );
}
