import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, ChevronDown, ChevronRight, Server } from "lucide-react";
import { cn } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import {
  listTabId,
  OVERVIEW_TAB_ID,
  typeKeyOf,
  useK8sStore,
  type K8sClusterState,
  type K8sResourceType,
} from "@/stores/k8sStore";
import { K8S_CATEGORIES, KindIcon, kindLabel, visibleTypes } from "./kindMeta";

interface Props {
  assetId: number;
  cluster: K8sClusterState;
}

const itemClass = (selected: boolean) =>
  cn(
    "flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-left text-xs",
    selected ? "bg-muted font-medium" : "hover:bg-muted/50"
  );

/** Sidebar navigation: cluster version, the fixed overview entry and the resource types grouped by category. */
export function K8sTypeNav({ assetId, cluster }: Props) {
  const { t } = useTranslation();
  const loadTypes = useK8sStore((s) => s.loadTypes);
  const toggleCategory = useK8sStore((s) => s.toggleCategory);
  const openTab = useK8sStore((s) => s.openTab);
  const setActiveTab = useK8sStore((s) => s.setActiveTab);
  const { version, types, activeTabId, collapsed } = cluster;
  const failedGroups = types.data?.failedGroups ?? [];
  const shown = types.data ? visibleTypes(types.data.types) : [];

  const openType = (type: K8sResourceType) =>
    openTab(assetId, { id: listTabId(typeKeyOf(type)), label: kindLabel(type, t) });

  const renderType = (type: K8sResourceType) => {
    const typeKey = typeKeyOf(type);
    return (
      <button
        key={typeKey}
        className={cn(itemClass(activeTabId === listTabId(typeKey)), "pl-5")}
        onClick={() => openType(type)}
      >
        <KindIcon typeKey={typeKey} className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate">{kindLabel(type, t)}</span>
      </button>
    );
  };

  return (
    <div className="min-h-0 flex-1 overflow-y-auto p-2">
      {version.data && (
        <p className="truncate px-2 pb-1 text-[11px] text-muted-foreground">
          {version.data.version} · {version.data.platform}
        </p>
      )}
      <button
        className={itemClass(activeTabId === OVERVIEW_TAB_ID)}
        onClick={() => setActiveTab(assetId, OVERVIEW_TAB_ID)}
      >
        <Server className="h-3.5 w-3.5" />
        {t("asset.k8sClusterOverview")}
      </button>

      {types.status === "error" ? (
        <AsyncState
          status="error"
          error={types.error}
          onRetry={() => void loadTypes(assetId)}
          className="items-start px-2 text-left"
        />
      ) : !types.data ? (
        <AsyncState status="loading" className="py-6" />
      ) : (
        <>
          {K8S_CATEGORIES.map((category) => {
            const inCategory = shown.filter((type) => type.category === category.key);
            if (inCategory.length === 0) return null;
            const isCollapsed = collapsed.includes(category.key);
            return (
              <div key={category.key} className="mt-1.5">
                <button
                  className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-[11px] font-medium text-muted-foreground hover:text-foreground"
                  aria-expanded={!isCollapsed}
                  onClick={() => toggleCategory(assetId, category.key)}
                >
                  {isCollapsed ? <ChevronRight className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
                  {t(category.labelKey)}
                </button>
                {!isCollapsed &&
                  (category.key === "custom" ? renderByGroup(inCategory, renderType) : inCategory.map(renderType))}
              </div>
            );
          })}
          {failedGroups.length > 0 && (
            <p
              className="mt-2 flex items-center gap-1 px-2 text-[11px] text-warning"
              title={failedGroups.map((g) => `${g.groupVersion}: ${g.error}`).join("\n")}
            >
              <AlertTriangle className="h-3 w-3 shrink-0" />
              {t("asset.k8sFailedGroups", { count: failedGroups.length })}
            </p>
          )}
        </>
      )}
    </div>
  );
}

/** Custom resources are listed under their API group. */
function renderByGroup(types: K8sResourceType[], renderType: (type: K8sResourceType) => React.ReactNode) {
  const groups = [...new Set(types.map((type) => type.group))].sort();
  return groups.map((group) => (
    <Fragment key={group}>
      <p className="truncate px-2 pl-7 pt-1 text-[11px] text-muted-foreground">{group || "core"}</p>
      {types.filter((type) => type.group === group).map(renderType)}
    </Fragment>
  ));
}
