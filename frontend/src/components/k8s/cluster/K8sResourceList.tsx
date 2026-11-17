import { useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { FilePlus2, RefreshCw, Search } from "lucide-react";
import { Button, Input, Switch, cn } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import {
  DEFAULT_LIST_VIEW,
  labelSelectorOf,
  listKey,
  resourceTabId,
  typeKeyOf,
  useK8sStore,
  type K8sClusterState,
  type K8sResourceRow,
} from "@/stores/k8sStore";
import { KindIcon, kindLabel } from "./kindMeta";
import { K8sResourceGrid } from "./K8sResourceGrid";
import { visibleRows } from "./listModel";

const AUTO_REFRESH_MS = 5000;
const SELECTOR_DEBOUNCE_MS = 300;

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
  const openYamlApply = useK8sStore((s) => s.openYamlApply);
  const setListView = useK8sStore((s) => s.setListView);
  const view = useK8sStore((s) => s.listViews[assetId]?.[typeKey]) ?? DEFAULT_LIST_VIEW;

  useEffect(() => {
    void loadList(assetId, typeKey, ns);
  }, [assetId, typeKey, ns, loadList]);

  // The label selector goes to the cluster, so a changed one re-reads the list — once typing pauses (the store
  // re-reads a list whose rows came from another selector).
  const selector = labelSelectorOf(view.filter);
  useEffect(() => {
    const timer = setTimeout(() => void loadList(assetId, typeKey, ns), SELECTOR_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [selector, assetId, typeKey, ns, loadList]);

  // The list tab is only mounted while it is the active inner tab of a mounted page, so the interval's
  // lifetime is exactly "visible".
  useEffect(() => {
    if (!view.autoRefresh) return;
    const timer = setInterval(() => {
      if (useK8sStore.getState().lists[assetId]?.[listKey(typeKey, ns)]?.reloading) return;
      void loadList(assetId, typeKey, ns, { force: true });
    }, AUTO_REFRESH_MS);
    return () => clearInterval(timer);
  }, [view.autoRefresh, assetId, typeKey, ns, loadList]);

  const reload = () => void loadList(assetId, typeKey, ns, { force: true });
  const busy = !entry || entry.status === "loading" || entry.reloading;
  const openRow = (row: K8sResourceRow) =>
    openTab(assetId, { id: resourceTabId(typeKey, row.namespace, row.name), label: row.name });

  const rows = useMemo(
    () => (entry ? visibleRows(entry.columns, entry.rows, view.filter, view.sort) : []),
    [entry, view.filter, view.sort]
  );
  // Same fallback as visibleRows: a sort on a column no longer listed shows as name ascending.
  const sort =
    view.sort && entry?.columns.some((c) => c.name === view.sort?.column)
      ? view.sort
      : { column: entry?.columns[0]?.name ?? "", dir: "asc" as const };
  const sortBy = (column: string) =>
    setListView(assetId, typeKey, {
      sort: sort.column === column ? { column, dir: sort.dir === "asc" ? "desc" : "asc" } : { column, dir: "asc" },
    });

  const empty =
    entry?.status === "ready" && rows.length === 0
      ? entry.rows.length > 0 || entry.labelSelector
        ? t("asset.k8sNoMatch")
        : ns
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
        {entry?.status === "ready" && <span className="text-[11px] text-muted-foreground">{rows.length}</span>}
        {namespace !== null && (
          <span className="rounded-sm border px-1.5 font-mono text-[11px] text-muted-foreground">
            {namespace || t("asset.k8sAllNamespaces")}
          </span>
        )}
        <div className="ml-auto flex items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={view.filter}
              onChange={(e) => setListView(assetId, typeKey, { filter: e.target.value })}
              placeholder={t("asset.k8sFilterPlaceholder")}
              aria-label={t("asset.k8sFilterPlaceholder")}
              className="h-7 w-56 pl-7 text-xs"
            />
            {entry?.filterError && (
              <p
                role="alert"
                className="absolute right-0 top-full z-20 mt-1 max-w-96 whitespace-pre-wrap break-words rounded-md border bg-popover px-2 py-1 font-mono text-xs text-destructive shadow-md select-text"
              >
                {entry.filterError}
              </p>
            )}
          </div>
          <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Switch
              checked={view.autoRefresh}
              onCheckedChange={(autoRefresh) => setListView(assetId, typeKey, { autoRefresh })}
              aria-label={t("asset.k8sAutoRefresh")}
              className="scale-75"
            />
            {t("asset.k8sAutoRefresh")}
          </label>
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => openYamlApply(assetId)}>
            <FilePlus2 className="h-3.5 w-3.5" />
            {t("asset.k8sApplyYaml")}
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            onClick={reload}
            disabled={busy}
            title={t("action.refresh")}
            aria-label={t("action.refresh")}
          >
            <RefreshCw className={cn("h-3.5 w-3.5", busy && "animate-spin")} />
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1">
        <AsyncState
          status={entry?.status ?? "loading"}
          error={entry?.error}
          onRetry={reload}
          empty={empty}
          className="h-full"
        >
          {entry && (
            <K8sResourceGrid
              assetId={assetId}
              typeKey={typeKey}
              columns={entry.columns}
              rows={rows}
              sort={sort}
              onSort={sortBy}
              onOpen={openRow}
            />
          )}
        </AsyncState>
      </div>
    </div>
  );
}
