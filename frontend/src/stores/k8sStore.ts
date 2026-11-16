import { create } from "zustand";
import { GetK8sClusterVersion, GetK8sPodDetail, GetK8sResourceTypes, ListK8sResources } from "../../wailsjs/go/k8s/K8s";
import { errorMessage } from "@/lib/errorMessage";
import type { PodDetail } from "@/components/k8s/cluster/types";
import type { LogTabState, LogTabStateUpdate } from "@/components/k8s/k8sLogState";
import { registerTabCloseHook, type PageTabMeta } from "./tabStore";

// ---------------------------------------------------------------- IPC shapes (internal/pkg/k8s/resources.go)

export type K8sCategory = "cluster" | "workloads" | "network" | "config" | "storage" | "rbac" | "custom";

export interface K8sResourceType {
  group: string;
  version: string;
  resource: string;
  kind: string;
  namespaced: boolean;
  verbs: string[];
  category: K8sCategory;
}

export interface K8sResourceTypes {
  types: K8sResourceType[];
  failedGroups: { groupVersion: string; error: string }[];
}

export interface K8sClusterVersion {
  version: string;
  platform: string;
}

export interface K8sTableColumn {
  name: string;
  type: string;
  format: string;
}

export interface K8sResourceRow {
  name: string;
  namespace: string;
  uid: string;
  creationTimestamp: string;
  labels: Record<string, string>;
  ownerReferences: { apiVersion: string; kind: string; name: string; controller: boolean }[];
  cells: unknown[];
  object?: Record<string, unknown>;
}

interface K8sResourceList {
  columns: K8sTableColumn[];
  rows: K8sResourceRow[];
}

// ---------------------------------------------------------------- store shapes

export type LoadStatus = "loading" | "error" | "ready";

/** One request-backed value. `data` survives a failed or in-flight reload so a refresh doesn't blank the region. */
export interface Loadable<T> {
  status: LoadStatus;
  data?: T;
  error: string | null;
}

/** A cached list, keyed `<typeKey>|<namespace>` (namespace '' = all namespaces / cluster-scoped). */
export interface K8sListEntry {
  status: LoadStatus;
  columns: K8sTableColumn[];
  rows: K8sResourceRow[];
  error: string | null;
  /** A forced re-read is in flight while the previous rows stay on screen. */
  reloading: boolean;
}

/**
 * Inner tab of the cluster page. Ids: `overview` | `list:<typeKey>` | `res:<typeKey>:<ns>:<name>` |
 * `term:<sessionKey>`; the legacy pod log tab is `log:<ns>:<pod>` until the generic detail replaces it.
 */
export interface K8sInnerTab {
  id: string;
  label: string;
}

export interface K8sClusterState {
  /** Selected namespace; '' = all namespaces. */
  namespace: string;
  version: Loadable<K8sClusterVersion>;
  types: Loadable<K8sResourceTypes>;
  namespaces: Loadable<string[]>;
  tabs: K8sInnerTab[];
  activeTabId: string;
  /** Collapsed sidebar category keys. */
  collapsed: string[];
  refreshing: boolean;
}

export const OVERVIEW_TAB_ID = "overview";

/** `<group>/<resource>`, core group ''. */
export function typeKeyOf(type: { group: string; resource: string }) {
  return `${type.group}/${type.resource}`;
}

export function listKey(typeKey: string, namespace: string) {
  return `${typeKey}|${namespace}`;
}

export function listTabId(typeKey: string) {
  return `list:${typeKey}`;
}

export function resourceTabId(typeKey: string, namespace: string, name: string) {
  return `res:${typeKey}:${namespace}:${name}`;
}

export function podLogTabId(namespace: string, pod: string) {
  return `log:${namespace}:${pod}`;
}

export function podDetailKey(namespace: string, pod: string) {
  return `${namespace}/${pod}`;
}

/** The pod detail a legacy pod detail (`res:/pods:<ns>:<pod>`) or log (`log:<ns>:<pod>`) tab shows. */
function podKeyOfTab(id: string): string | null {
  const rest = id.startsWith("res:/pods:") ? id.slice("res:/pods:".length) : id.startsWith("log:") ? id.slice(4) : null;
  if (rest === null) return null;
  const [namespace, pod] = rest.split(":");
  return podDetailKey(namespace, pod);
}

type Refresher = () => Promise<unknown>;

interface K8sStoreState {
  clusters: Record<number, K8sClusterState>;
  lists: Record<number, Record<string, K8sListEntry>>;
  /** Legacy pod detail (GetK8sPodDetail) keyed `<ns>/<pod>`, until the generic detail replaces it. */
  podDetails: Record<number, Record<string, Loadable<PodDetail>>>;
  logTabs: Record<number, Record<string, LogTabState>>;

  /** Creates the cluster's state and reads it; a cluster that already has state (page remount) is left as is. */
  initCluster: (assetId: number, defaultNamespace: string) => void;
  loadVersion: (assetId: number) => Promise<void>;
  loadTypes: (assetId: number) => Promise<void>;
  loadNamespaces: (assetId: number) => Promise<void>;
  setNamespace: (assetId: number, namespace: string) => void;
  toggleCategory: (assetId: number, key: string) => void;
  /** Reads a list unless it is cached (or `force`); pass namespace '' for cluster-scoped types and all namespaces. */
  loadList: (assetId: number, typeKey: string, namespace: string, opts?: { force?: boolean }) => Promise<void>;
  openTab: (assetId: number, tab: K8sInnerTab) => void;
  setActiveTab: (assetId: number, id: string) => void;
  /** Closes tabs (the overview tab never closes); `activate` picks the tab to show afterwards. */
  closeTabs: (assetId: number, ids: string[], activate?: string) => void;
  /** Re-reads version, types, namespaces, every loaded list / detail and every registered view. */
  refreshAll: (assetId: number) => Promise<void>;
  /** Lets a view take part in `refreshAll`; returns the unregister function. */
  registerRefresher: (assetId: number, key: string, fn: Refresher) => () => void;
  loadPodDetail: (assetId: number, namespace: string, pod: string, opts?: { force?: boolean }) => Promise<void>;
  openPodLogs: (assetId: number, namespace: string, pod: string, container: string, label: string) => void;
  updateLogTab: (assetId: number, tabId: string, update: LogTabStateUpdate) => void;
  clearCluster: (assetId: number) => void;
}

// ---------------------------------------------------------------- request bookkeeping

// Latest request token per `<assetId>|<slot>`: a response only lands if it is still the newest request for
// its slot, so an older read finishing late can't overwrite a newer one, and reads still in flight when the
// cluster tab closes are dropped (clearCluster forgets the tokens).
const tokens = new Map<string, number>();
let tokenSeq = 0;

function begin(assetId: number, slot: string) {
  const token = ++tokenSeq;
  tokens.set(`${assetId}|${slot}`, token);
  return token;
}

function isCurrent(assetId: number, slot: string, token: number) {
  return tokens.get(`${assetId}|${slot}`) === token;
}

function forgetTokens(assetId: number) {
  for (const key of tokens.keys()) {
    if (key.startsWith(`${assetId}|`)) tokens.delete(key);
  }
}

const refreshers = new Map<number, Map<string, Refresher>>();

/**
 * State of a value being (re)read: a value already shown stays as is while it reloads (the refresh control
 * spins instead); anything else — never read, or failed — shows as loading.
 */
function pending<L extends Loadable<unknown>>(prev: L | undefined): L {
  return prev?.status === "ready" ? { ...prev } : ({ ...prev, status: "loading", error: null } as L);
}

function newCluster(namespace: string): K8sClusterState {
  return {
    namespace,
    version: { status: "loading", error: null },
    types: { status: "loading", error: null },
    namespaces: { status: "loading", error: null },
    tabs: [{ id: OVERVIEW_TAB_ID, label: "" }],
    activeTabId: OVERVIEW_TAB_ID,
    collapsed: [],
    refreshing: false,
  };
}

function apiVersionOf(cluster: K8sClusterState, typeKey: string): string | undefined {
  const [group] = typeKey.split("/");
  // The core group is served only as /api/v1, so it doesn't depend on discovery having loaded.
  if (group === "") return "v1";
  return cluster.types.data?.types.find((t) => typeKeyOf(t) === typeKey)?.version;
}

export const useK8sStore = create<K8sStoreState>((set, get) => {
  const patchCluster = (assetId: number, patch: (c: K8sClusterState) => Partial<K8sClusterState>) => {
    set((s) => {
      const cluster = s.clusters[assetId];
      if (!cluster) return s;
      return { clusters: { ...s.clusters, [assetId]: { ...cluster, ...patch(cluster) } } };
    });
  };

  /** Reads one cluster-level value into `field`, keeping old data visible while it reloads. */
  async function loadField<K extends "version" | "types" | "namespaces">(
    assetId: number,
    field: K,
    read: () => Promise<K8sClusterState[K]["data"]>
  ) {
    const token = begin(assetId, field);
    patchCluster(assetId, (c) => ({ [field]: pending(c[field]) }));
    try {
      const data = await read();
      if (!isCurrent(assetId, field, token)) return;
      patchCluster(assetId, () => ({ [field]: { status: "ready", data, error: null } }));
    } catch (e) {
      if (!isCurrent(assetId, field, token)) return;
      patchCluster(assetId, (c) => ({ [field]: { ...c[field], status: "error", error: errorMessage(e) } }));
    }
  }

  return {
    clusters: {},
    lists: {},
    podDetails: {},
    logTabs: {},

    initCluster: (assetId, defaultNamespace) => {
      if (get().clusters[assetId]) return;
      set((s) => ({ clusters: { ...s.clusters, [assetId]: newCluster(defaultNamespace) } }));
      void get().loadVersion(assetId);
      void get().loadTypes(assetId);
      void get().loadNamespaces(assetId);
    },

    loadVersion: (assetId) =>
      loadField(assetId, "version", async () => JSON.parse(await GetK8sClusterVersion(assetId)) as K8sClusterVersion),

    loadTypes: (assetId) =>
      loadField(assetId, "types", async () => JSON.parse(await GetK8sResourceTypes(assetId)) as K8sResourceTypes),

    loadNamespaces: (assetId) =>
      loadField(assetId, "namespaces", async () => {
        const result = await ListK8sResources({
          assetId,
          group: "",
          version: "v1",
          resource: "namespaces",
          namespace: "",
          labelSelector: "",
          fieldSelector: "",
          includeObject: false,
        });
        return (JSON.parse(result) as K8sResourceList).rows.map((r) => r.name);
      }),

    setNamespace: (assetId, namespace) => patchCluster(assetId, () => ({ namespace })),

    toggleCategory: (assetId, key) =>
      patchCluster(assetId, (c) => ({
        collapsed: c.collapsed.includes(key) ? c.collapsed.filter((k) => k !== key) : [...c.collapsed, key],
      })),

    loadList: async (assetId, typeKey, namespace, opts) => {
      const cluster = get().clusters[assetId];
      if (!cluster) return;
      const key = listKey(typeKey, namespace);
      const prev = get().lists[assetId]?.[key];
      if (prev && !opts?.force) return;

      const setEntry = (entry: K8sListEntry) =>
        set((s) => {
          if (!s.clusters[assetId]) return s;
          return { lists: { ...s.lists, [assetId]: { ...s.lists[assetId], [key]: entry } } };
        });

      const slot = `list:${key}`;
      const token = begin(assetId, slot);
      setEntry(
        prev?.status === "ready"
          ? { ...prev, reloading: true }
          : { status: "loading", columns: [], rows: [], error: null, reloading: false }
      );
      const version = apiVersionOf(cluster, typeKey);
      const [group, resource] = typeKey.split("/");
      try {
        if (!version) throw new Error(`unknown resource type ${typeKey}`);
        const result = await ListK8sResources({
          assetId,
          group,
          version,
          resource,
          namespace,
          labelSelector: "",
          fieldSelector: "",
          includeObject: false,
        });
        if (!isCurrent(assetId, slot, token)) return;
        const data = JSON.parse(result) as K8sResourceList;
        setEntry({ status: "ready", columns: data.columns, rows: data.rows, error: null, reloading: false });
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({
          status: "error",
          columns: prev?.columns ?? [],
          rows: prev?.rows ?? [],
          error: errorMessage(e),
          reloading: false,
        });
      }
    },

    openTab: (assetId, tab) =>
      patchCluster(assetId, (c) => ({
        tabs: c.tabs.some((t) => t.id === tab.id) ? c.tabs : [...c.tabs, tab],
        activeTabId: tab.id,
      })),

    setActiveTab: (assetId, id) => patchCluster(assetId, () => ({ activeTabId: id })),

    closeTabs: (assetId, ids, activate) => {
      const closing = new Set(ids.filter((id) => id !== OVERVIEW_TAB_ID));
      patchCluster(assetId, (c) => {
        const tabs = c.tabs.filter((t) => !closing.has(t.id));
        let activeTabId = activate ?? c.activeTabId;
        if (closing.has(activeTabId)) {
          const idx = c.tabs.findIndex((t) => t.id === activeTabId);
          const neighbour = [...c.tabs.slice(idx + 1), ...c.tabs.slice(0, idx).reverse()].find(
            (t) => !closing.has(t.id)
          );
          activeTabId = neighbour?.id ?? OVERVIEW_TAB_ID;
        }
        return { tabs, activeTabId };
      });
      set((s) => {
        const cluster = s.clusters[assetId];
        if (!cluster) return s;
        const logs = { ...s.logTabs[assetId] };
        for (const id of closing) delete logs[id];
        // A pod detail is read for its detail / log tabs; once none is open it is dropped, so opening it again
        // reads it afresh and the top refresh doesn't keep re-reading it.
        const shown = new Set(cluster.tabs.map((t) => podKeyOfTab(t.id)));
        const details: Record<string, Loadable<PodDetail>> = {};
        for (const [key, entry] of Object.entries(s.podDetails[assetId] ?? {})) {
          if (shown.has(key)) details[key] = entry;
          else tokens.delete(`${assetId}|pod:${key}`); // a read still in flight must not bring it back
        }
        return {
          logTabs: { ...s.logTabs, [assetId]: logs },
          podDetails: { ...s.podDetails, [assetId]: details },
        };
      });
    },

    refreshAll: async (assetId) => {
      if (!get().clusters[assetId]) return;
      patchCluster(assetId, () => ({ refreshing: true }));
      const state = get();
      const reads: Promise<unknown>[] = [
        state.loadVersion(assetId),
        state.loadTypes(assetId),
        state.loadNamespaces(assetId),
      ];
      for (const key of Object.keys(state.lists[assetId] ?? {})) {
        const sep = key.lastIndexOf("|");
        reads.push(state.loadList(assetId, key.slice(0, sep), key.slice(sep + 1), { force: true }));
      }
      for (const key of Object.keys(state.podDetails[assetId] ?? {})) {
        const sep = key.indexOf("/");
        reads.push(state.loadPodDetail(assetId, key.slice(0, sep), key.slice(sep + 1), { force: true }));
      }
      for (const fn of refreshers.get(assetId)?.values() ?? []) reads.push(fn());
      await Promise.allSettled(reads);
      patchCluster(assetId, () => ({ refreshing: false }));
    },

    registerRefresher: (assetId, key, fn) => {
      let byKey = refreshers.get(assetId);
      if (!byKey) {
        byKey = new Map();
        refreshers.set(assetId, byKey);
      }
      byKey.set(key, fn);
      return () => {
        if (refreshers.get(assetId)?.get(key) === fn) refreshers.get(assetId)?.delete(key);
      };
    },

    loadPodDetail: async (assetId, namespace, pod, opts) => {
      if (!get().clusters[assetId]) return;
      const key = podDetailKey(namespace, pod);
      const prev = get().podDetails[assetId]?.[key];
      if (prev && !opts?.force) return;
      const setEntry = (entry: Loadable<PodDetail>) =>
        set((s) => {
          if (!s.clusters[assetId]) return s;
          return { podDetails: { ...s.podDetails, [assetId]: { ...s.podDetails[assetId], [key]: entry } } };
        });
      const slot = `pod:${key}`;
      const token = begin(assetId, slot);
      setEntry(pending(prev));
      try {
        const data = JSON.parse(await GetK8sPodDetail(assetId, namespace, pod)) as PodDetail;
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({ status: "ready", data, error: null });
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({ ...pending(prev), status: "error", error: errorMessage(e) });
      }
    },

    openPodLogs: (assetId, namespace, pod, container, label) => {
      const id = podLogTabId(namespace, pod);
      set((s) => {
        if (!s.clusters[assetId] || s.logTabs[assetId]?.[id]) return s;
        const initial: LogTabState = {
          logStreamID: null,
          logContainer: container,
          logTailLines: 200,
          logError: null,
          logBuffers: {},
        };
        return { logTabs: { ...s.logTabs, [assetId]: { ...s.logTabs[assetId], [id]: initial } } };
      });
      get().openTab(assetId, { id, label });
    },

    updateLogTab: (assetId, tabId, update) =>
      set((s) => {
        const existing = s.logTabs[assetId]?.[tabId];
        // The log panel reports its stream stopping when it unmounts, which also happens right after its
        // tab (or the whole cluster tab) closed — that state is gone and must stay gone.
        if (!existing) return s;
        const next = typeof update === "function" ? update(existing) : { ...existing, ...update };
        return { logTabs: { ...s.logTabs, [assetId]: { ...s.logTabs[assetId], [tabId]: next } } };
      }),

    clearCluster: (assetId) => {
      forgetTokens(assetId);
      refreshers.delete(assetId);
      set((s) => {
        const without = <T>(map: Record<number, T>) => {
          const next = { ...map };
          delete next[assetId];
          return next;
        };
        return {
          clusters: without(s.clusters),
          lists: without(s.lists),
          podDetails: without(s.podDetails),
          logTabs: without(s.logTabs),
        };
      });
    },
  };
});

export const K8S_CLUSTER_PAGE_ID = "k8s-cluster";

// The cluster page unmounts on every top-level tab switch, so its state lives here until its tab closes.
registerTabCloseHook((tab) => {
  if (tab.type !== "page") return;
  const meta = tab.meta as PageTabMeta;
  if (meta.pageId !== K8S_CLUSTER_PAGE_ID || meta.assetId == null) return;
  useK8sStore.getState().clearCluster(meta.assetId);
});
