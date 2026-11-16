import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { RefreshCw } from "lucide-react";
import { Button, cn } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import {
  listKey,
  resourceTabId,
  typeKeyOf,
  useK8sStore,
  type K8sClusterState,
  type K8sResourceRow,
} from "@/stores/k8sStore";
import { KindIcon, kindLabel } from "./kindMeta";
import { K8sResourceTable } from "./K8sResourceTable";

// Pod rows open the legacy pod detail until the generic resource detail replaces it.
const POD_TYPE_KEY = "/pods";

interface Props {
  assetId: number;
  cluster: K8sClusterState;
  typeKey: string;
}

/** List tab of one resource type, read for the selected namespace (cluster-scoped types ignore it). */
export function K8sResourceList({ assetId, cluster, typeKey }: Props) {
  const { t } = useTranslation();
  const loadTypes = useK8sStore((s) => s.loadTypes);
  const type = cluster.types.data?.types.find((candidate) => typeKeyOf(candidate) === typeKey);

  if (!type) {
    if (cluster.types.status === "error") {
      return (
        <AsyncState
          status="error"
          error={cluster.types.error}
          onRetry={() => void loadTypes(assetId)}
          className="h-full"
        />
      );
    }
    if (!cluster.types.data) return <AsyncState status="loading" className="h-full" />;
    return <AsyncState status="error" error={t("asset.k8sTypeUnavailable", { type: typeKey })} className="h-full" />;
  }

  return (
    <LoadedTypeList
      assetId={assetId}
      typeKey={typeKey}
      label={kindLabel(type, t)}
      apiVersion={type.category === "custom" ? `${type.group || "core"}/${type.version}` : null}
      namespace={type.namespaced ? cluster.namespace : null}
    />
  );
}

function LoadedTypeList({
  assetId,
  typeKey,
  label,
  apiVersion,
  namespace,
}: {
  assetId: number;
  typeKey: string;
  label: string;
  /** Shown for custom resources. */
  apiVersion: string | null;
  /** Selected namespace for a namespaced type ('' = all); null for a cluster-scoped type. */
  namespace: string | null;
}) {
  const { t } = useTranslation();
  const ns = namespace ?? "";
  const entry = useK8sStore((s) => s.lists[assetId]?.[listKey(typeKey, ns)]);
  const loadList = useK8sStore((s) => s.loadList);
  const openTab = useK8sStore((s) => s.openTab);

  useEffect(() => {
    void loadList(assetId, typeKey, ns);
  }, [assetId, typeKey, ns, loadList]);

  const reload = () => void loadList(assetId, typeKey, ns, { force: true });
  const busy = !entry || entry.status === "loading" || entry.reloading;
  const openRow =
    typeKey === POD_TYPE_KEY
      ? (row: K8sResourceRow) =>
          openTab(assetId, { id: resourceTabId(typeKey, row.namespace, row.name), label: row.name })
      : undefined;
  const empty =
    entry?.status === "ready" && entry.rows.length === 0
      ? ns
        ? t("asset.k8sEmptyInNamespace", { namespace: ns, kind: label })
        : t("asset.k8sEmpty", { kind: label })
      : undefined;

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        <KindIcon typeKey={typeKey} className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium">{label}</span>
        {apiVersion && (
          <span className="rounded-sm bg-muted px-1.5 font-mono text-[11px] text-muted-foreground">{apiVersion}</span>
        )}
        {entry?.status === "ready" && <span className="text-[11px] text-muted-foreground">{entry.rows.length}</span>}
        {namespace !== null && (
          <span className="rounded-sm border px-1.5 font-mono text-[11px] text-muted-foreground">
            {namespace || t("asset.k8sAllNamespaces")}
          </span>
        )}
        <Button
          variant="ghost"
          size="icon"
          className="ml-auto h-7 w-7"
          onClick={reload}
          disabled={busy}
          title={t("action.refresh")}
          aria-label={t("action.refresh")}
        >
          <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <AsyncState
          status={entry?.status ?? "loading"}
          error={entry?.error}
          onRetry={reload}
          empty={empty}
          className="h-full"
        >
          {entry && <K8sResourceTable columns={entry.columns} rows={entry.rows} onOpen={openRow} />}
        </AsyncState>
      </div>
    </div>
  );
}
