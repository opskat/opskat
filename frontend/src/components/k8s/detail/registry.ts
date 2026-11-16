import { createElement, type ComponentType, type ReactNode } from "react";
import type { K8sActionContext } from "../actions";

/** What a kind-specific overview / sub-tab renders; `object` is the resource as last read. */
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

const overviews = new Map<string, ComponentType<K8sDetailProps>>();
const subTabs = new Map<string, K8sDetailSubTab[]>();
let yamlEditHandler: ((ctx: K8sActionContext) => void) | null = null;

/** Replaces the generic overview of a Kind (e.g. "Deployment"); returns the unregister function. */
export function registerDetailOverview(kind: string, Component: ComponentType<K8sDetailProps>): () => void {
  overviews.set(kind, Component);
  return () => {
    if (overviews.get(kind) === Component) overviews.delete(kind);
  };
}

/** The registered overview of a Kind rendered, or `fallback` when it has none. */
export function renderDetailOverview(
  kind: string,
  props: K8sDetailProps,
  fallback: ComponentType<K8sDetailProps>
): ReactNode {
  return createElement(overviews.get(kind) ?? fallback, props);
}

/** Adds a sub-tab (between the overview and the events tab) to the detail of a Kind; returns the unregister function. */
export function registerDetailSubTab(kind: string, tab: K8sDetailSubTab): () => void {
  subTabs.set(kind, [...(subTabs.get(kind) ?? []), tab]);
  return () =>
    subTabs.set(
      kind,
      (subTabs.get(kind) ?? []).filter((t) => t !== tab)
    );
}

export function getDetailSubTabs(kind: string): K8sDetailSubTab[] {
  return subTabs.get(kind) ?? [];
}

/** The YAML sub-tab shows an 「编辑」 button only while a handler (the edit dialog) is registered. */
export function registerYamlEditHandler(handler: (ctx: K8sActionContext) => void): () => void {
  yamlEditHandler = handler;
  return () => {
    if (yamlEditHandler === handler) yamlEditHandler = null;
  };
}

export function getYamlEditHandler(): ((ctx: K8sActionContext) => void) | null {
  return yamlEditHandler;
}
