import { createElement, type ComponentType, type ReactNode } from "react";
import { deriveStatus, type DerivedStatus } from "./status";

/** What a type-specific overview / sub-tab renders; `object` is the resource as last read. */
export interface K8sDetailProps {
  assetId: number;
  typeKey: string;
  namespace: string;
  name: string;
  object: Record<string, unknown>;
}

export interface K8sDetailSubTab {
  id: string;
  labelKey: string;
  Component: ComponentType<K8sDetailProps>;
}

// Keyed by type key (`<group>/<resource>`), not Kind: a custom resource may reuse a built-in Kind name
// (a Knative Service, a Volcano Job) and must not get the built-in's views.
const overviews = new Map<string, ComponentType<K8sDetailProps>>();
const subTabs = new Map<string, K8sDetailSubTab[]>();
const statuses = new Map<string, (object: Record<string, unknown>) => DerivedStatus | null>();

/** Replaces the generic overview of a type (e.g. "apps/deployments"); returns the unregister function. */
export function registerDetailOverview(typeKey: string, Component: ComponentType<K8sDetailProps>): () => void {
  overviews.set(typeKey, Component);
  return () => {
    if (overviews.get(typeKey) === Component) overviews.delete(typeKey);
  };
}

/** The registered overview of a type rendered, or `fallback` when it has none. */
export function renderDetailOverview(
  typeKey: string,
  props: K8sDetailProps,
  fallback: ComponentType<K8sDetailProps>
): ReactNode {
  return createElement(overviews.get(typeKey) ?? fallback, props);
}

/** Adds a sub-tab (between the overview and the events tab) to the detail of a type; returns the unregister function. */
export function registerDetailSubTab(typeKey: string, tab: K8sDetailSubTab): () => void {
  subTabs.set(typeKey, [...(subTabs.get(typeKey) ?? []), tab]);
  return () =>
    subTabs.set(
      typeKey,
      (subTabs.get(typeKey) ?? []).filter((t) => t !== tab)
    );
}

export function getDetailSubTabs(typeKey: string): K8sDetailSubTab[] {
  return subTabs.get(typeKey) ?? [];
}

/** Replaces the generic header status of a type (the one `kubectl get` prints); returns the unregister function. */
export function registerDetailStatus(
  typeKey: string,
  status: (object: Record<string, unknown>) => DerivedStatus | null
): () => void {
  statuses.set(typeKey, status);
  return () => {
    if (statuses.get(typeKey) === status) statuses.delete(typeKey);
  };
}

/** The header status of a resource: its type's registered one, else the generic one. */
export function detailStatusOf(typeKey: string, object: Record<string, unknown>): DerivedStatus | null {
  return (statuses.get(typeKey) ?? deriveStatus)(object);
}
