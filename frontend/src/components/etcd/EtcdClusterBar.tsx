import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { RefreshCw, Loader2 } from "lucide-react";
import { useEtcdStore } from "@/stores/etcdStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";

export interface EtcdClusterBarProps {
  assetId: number;
}

export function EtcdClusterBar({ assetId }: EtcdClusterBarProps) {
  const { t } = useTranslation();
  const clusterInfo = useEtcdStore((s) => s.clusterInfo.get(assetId));
  const loadClusterInfo = useEtcdStore((s) => s.loadClusterInfo);

  // 资产名称：复用 QueryTabMeta.assetName，落到 label 兜底
  const assetLabel = useTabStore((s) => {
    const tab = s.tabs.find((tt) => {
      const meta = tt.meta as { assetId?: number } | undefined;
      return meta?.assetId === assetId;
    });
    if (!tab) return `asset#${assetId}`;
    if (tab.meta.type === "query") return (tab.meta as QueryTabMeta).assetName || tab.label;
    return tab.label || `asset#${assetId}`;
  });

  useEffect(() => {
    loadClusterInfo(assetId).catch((e: unknown) => {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`${t("etcd.cluster.loadFailed")}: ${msg}`);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetId, loadClusterInfo]);

  const status = clusterInfo?.status ?? "unknown";
  const dotColor =
    status === "healthy"
      ? "bg-emerald-500"
      : status === "loading"
        ? "bg-slate-400 animate-pulse"
        : status === "unhealthy"
          ? "bg-destructive"
          : "bg-muted-foreground/50";
  const statusLabel =
    status === "healthy"
      ? t("etcd.cluster.healthy")
      : status === "unhealthy"
        ? t("etcd.cluster.unhealthy")
        : t("etcd.cluster.unknown");

  function refresh() {
    loadClusterInfo(assetId, { force: true }).catch((e: unknown) => {
      const msg = e instanceof Error ? e.message : String(e);
      toast.error(`${t("etcd.cluster.loadFailed")}: ${msg}`);
    });
  }

  return (
    <div
      className="flex h-9 shrink-0 items-center gap-2 border-b bg-muted/20 px-3 text-xs"
      data-testid="etcd-cluster-bar"
    >
      <span className={`size-2 rounded-full ${dotColor}`} title={statusLabel} />
      <span className="font-medium" data-testid="etcd-cluster-name">
        {assetLabel}
      </span>
      <span className="text-muted-foreground">·</span>
      <span className="text-muted-foreground">
        {t("etcd.cluster.endpoints", { count: clusterInfo?.memberCount ?? 0 })}
      </span>
      {clusterInfo?.error && (
        <>
          <span className="text-muted-foreground">·</span>
          <span className="truncate text-destructive" title={clusterInfo.error}>
            {clusterInfo.error}
          </span>
        </>
      )}
      <div className="flex-1" />
      <button
        type="button"
        className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        onClick={refresh}
        title={t("etcd.cluster.refresh")}
        data-testid="etcd-cluster-refresh"
        disabled={status === "loading"}
      >
        {status === "loading" ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
      </button>
    </div>
  );
}
