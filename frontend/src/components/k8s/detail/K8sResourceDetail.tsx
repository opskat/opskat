import { createElement, useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Tabs, TabsContent, TabsList, TabsTrigger, Button, cn } from "@opskat/ui";
import { AsyncState } from "@/components/common/AsyncState";
import {
  parseResourceTabId,
  resourceTabId,
  typeKeyOf,
  useK8sStore,
  type K8sClusterState,
  type K8sResourceType,
} from "@/stores/k8sStore";
import { actionLabelKey, getK8sActions, type K8sActionContext } from "../actions";
import { KindIcon } from "../cluster/kindMeta";
import { statusVariantToClass } from "../utils";
import { K8sEventsTab } from "./K8sEventsTab";
import { K8sGenericOverview } from "./K8sGenericOverview";
import { K8sYamlTab } from "./K8sYamlTab";
import { detailStatusOf, renderDetailOverview, getDetailSubTabs, type K8sDetailProps } from "./registry";
import { K8sStaleError } from "../K8sAsyncState";

interface OwnerRef {
  apiVersion: string;
  kind: string;
  name: string;
  controller?: boolean;
}

/** The controller owner, else the first one. */
function primaryOwner(object: Record<string, unknown>): OwnerRef | undefined {
  const owners = ((object.metadata as { ownerReferences?: OwnerRef[] } | undefined)?.ownerReferences ??
    []) as OwnerRef[];
  return owners.find((o) => o.controller) ?? owners[0];
}

/** The discovered type an owner reference points at (group from its apiVersion + Kind). */
function ownerType(owner: OwnerRef, types: K8sResourceType[] | undefined): K8sResourceType | undefined {
  const group = owner.apiVersion.includes("/") ? owner.apiVersion.split("/")[0] : "";
  return types?.find((t) => t.group === group && t.kind === owner.kind);
}

/** Detail tab of any resource: header with registered actions, then overview / events / YAML (plus registered sub-tabs). */
export function K8sResourceDetail({
  assetId,
  cluster,
  tabId,
}: {
  assetId: number;
  cluster: K8sClusterState;
  tabId: string;
}) {
  const { t } = useTranslation();
  const entry = useK8sStore((s) => s.details[assetId]?.[tabId]);
  const loadDetail = useK8sStore((s) => s.loadDetail);
  const openTab = useK8sStore((s) => s.openTab);
  const subTab = useK8sStore((s) => s.detailSubTabs[assetId]?.[tabId]) ?? "overview";
  const setDetailSubTab = useK8sStore((s) => s.setDetailSubTab);

  const ref = parseResourceTabId(tabId);
  // Reads only when the detail isn't cached yet (first open); inner-tab switches never come through here.
  useEffect(() => {
    void loadDetail(assetId, tabId);
  }, [assetId, tabId, loadDetail]);

  const reload = () => void loadDetail(assetId, tabId, { force: true });
  const resource = entry?.resource;
  const detail = resource?.data;
  const object = detail?.object ?? null;
  const warnings = useMemo(() => entry?.events.data?.filter((e) => e.type === "Warning").length, [entry?.events.data]);

  if (!ref) return null;
  const { typeKey, namespace, name } = ref;
  const types = cluster.types.data?.types;

  if (detail?.notFound) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        {t("asset.k8sResourceGone")}
      </div>
    );
  }
  if (!detail || !object || !entry) {
    return (
      <AsyncState
        status={resource?.status === "error" ? "error" : "loading"}
        error={resource?.error}
        onRetry={reload}
        className="h-full"
      />
    );
  }

  const kind = (object.kind as string | undefined) ?? types?.find((x) => typeKeyOf(x) === typeKey)?.kind ?? "";
  const status = detailStatusOf(typeKey, object);
  const created = (object.metadata as { creationTimestamp?: string } | undefined)?.creationTimestamp;
  const owner = primaryOwner(object);
  const ownerKey = owner && ownerType(owner, types);
  const ctx: K8sActionContext = { assetId, typeKey, namespace, name, object };
  const actions = getK8sActions(typeKey, object, assetId);
  const props: K8sDetailProps = { assetId, typeKey, namespace, name, object };
  const extraTabs = getDetailSubTabs(typeKey);
  const editYaml = actions.find((a) => a.id === "edit-yaml");
  const triggerClass =
    "h-full flex-none gap-1.5 rounded-none px-2.5 text-xs font-normal after:bg-primary group-data-[orientation=horizontal]/tabs:after:bottom-0";

  return (
    <div className="flex h-full flex-col">
      {resource.status === "error" && <K8sStaleError error={resource.error} onRetry={reload} className="m-2 mb-0" />}
      <div data-testid="k8s-detail-header" className="flex shrink-0 items-start justify-between gap-4 border-b p-4">
        <div className="flex min-w-0 items-start gap-3">
          <KindIcon typeKey={typeKey} className="mt-0.5 h-5 w-5 shrink-0 text-muted-foreground" />
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="truncate font-mono text-sm font-medium">{name}</h3>
              {status && (
                <span
                  className={cn("rounded-full px-2 py-0.5 text-xs font-medium", statusVariantToClass(status.variant))}
                >
                  {status.text}
                </span>
              )}
            </div>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
              <span>{namespace ? `${kind} · ${namespace}` : kind}</span>
              {owner && (
                <span>
                  {t("asset.k8sOwner")}{" "}
                  {ownerKey ? (
                    <button
                      type="button"
                      role="link"
                      className="font-mono text-primary hover:underline"
                      onClick={() =>
                        openTab(assetId, {
                          id: resourceTabId(typeKeyOf(ownerKey), ownerKey.namespaced ? namespace : "", owner.name),
                          label: owner.name,
                        })
                      }
                    >
                      {owner.kind}/{owner.name}
                    </button>
                  ) : (
                    <span className="font-mono">
                      {owner.kind}/{owner.name}
                    </span>
                  )}
                </span>
              )}
              {created && <span>{new Date(created).toLocaleString()}</span>}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {actions.map((a) => {
            const reason = a.disabledReason?.(object);
            return (
              // The wrapper carries the reason: a disabled button gets no hover.
              <span key={a.id} title={reason}>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!!reason}
                  className={cn(a.destructive && "text-destructive hover:text-destructive")}
                  onClick={() => void a.run(ctx)}
                >
                  <a.icon className="h-3 w-3" />
                  {t(actionLabelKey(a, object))}
                </Button>
              </span>
            );
          })}
        </div>
      </div>

      <Tabs value={subTab} onValueChange={(v) => setDetailSubTab(assetId, tabId, v)} className="min-h-0 flex-1 gap-0">
        <div className="flex h-9 w-full shrink-0 items-center border-b border-border bg-muted/20 px-2">
          <TabsList variant="line" className="h-full justify-start bg-transparent p-0">
            <TabsTrigger value="overview" className={triggerClass}>
              {t("asset.k8sOverview")}
            </TabsTrigger>
            {extraTabs.map((x) => (
              <TabsTrigger key={x.id} value={x.id} className={triggerClass}>
                {t(x.labelKey)}
              </TabsTrigger>
            ))}
            <TabsTrigger value="events" className={triggerClass}>
              {t("asset.k8sEvents")}
              {!!warnings && (
                <span className="rounded bg-warning/15 px-1 text-[11px] tabular-nums text-warning">{warnings}</span>
              )}
            </TabsTrigger>
            <TabsTrigger value="yaml" className={triggerClass}>
              YAML
            </TabsTrigger>
          </TabsList>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          <TabsContent value="overview">{renderDetailOverview(typeKey, props, K8sGenericOverview)}</TabsContent>
          {extraTabs.map((x) => (
            <TabsContent key={x.id} value={x.id}>
              {createElement(x.Component, props)}
            </TabsContent>
          ))}
          <TabsContent value="events">
            <K8sEventsTab events={entry.events} onRetry={reload} />
          </TabsContent>
          <TabsContent value="yaml">
            <K8sYamlTab yaml={detail.yaml} onEdit={editYaml && (() => void editYaml.run(ctx))} />
          </TabsContent>
        </div>
      </Tabs>
    </div>
  );
}
