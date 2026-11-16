import { create } from "zustand";
import {
  CountK8sResources,
  GetK8sClusterHealth,
  GetK8sClusterVersion,
  GetK8sResource,
  GetK8sResourceTypes,
  ListK8sEvents,
  ListK8sResources,
} from "../../wailsjs/go/k8s/K8s";
import { errorMessage } from "@/lib/errorMessage";
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
  /** The object's status.phase, on lists read with `withPhase` (Pods). */
  phase?: string;
  object?: Record<string, unknown>;
}

/** GetK8sResource result; `notFound` = deleted since it was opened (object / yaml are then empty). */
export interface K8sResourceDetail {
  notFound: boolean;
  object: Record<string, unknown> | null;
  yaml: string;
}

export interface K8sEvent {
  type: string;
  reason: string;
  message: string;
  count: number;
  lastTimestamp: string;
  involvedObject: { apiVersion: string; kind: string; namespace: string; name: string; uid: string };
}

/** One row of the overview's 需要关注 list (internal/pkg/k8s/health.go). */
export interface K8sAttentionItem {
  group: string;
  version: string;
  resource: string;
  kind: string;
  namespace: string;
  name: string;
  status: string;
  reason: string;
}

/** GetK8sClusterHealth result: each section fails on its own (`errors`), a failed type has no abnormal count. */
export interface K8sClusterHealth {
  nodes: { total: number; schedulable: number; unschedulable: number; notReady: number; error?: string };
  pods: { total: number; running: number; pending: number; abnormal: number; error?: string };
  attention: K8sAttentionItem[];
  abnormalCounts: Record<string, number>;
  warningEvents: number;
  errors: Record<string, string>;
}

/** A sidebar count: `error` is the reason it could not be read (shown as the hover text of "—"). */
export interface K8sCount {
  status: LoadStatus;
  count?: number;
  error: string | null;
}

/** One open detail tab: the resource and its events are separate regions so either can fail alone. */
export interface K8sDetailEntry {
  resource: Loadable<K8sResourceDetail>;
  events: Loadable<K8sEvent[]>;
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
  /** The cluster's reason for rejecting the label selector; the previous rows stay on screen. */
  filterError: string | null;
}

export interface K8sListSort {
  /** Server column name. */
  column: string;
  dir: "asc" | "desc";
}

/** What a list tab remembers while its page is unmounted; keyed by typeKey. `sort` null = name ascending. */
export interface K8sListView {
  filter: string;
  sort: K8sListSort | null;
  autoRefresh: boolean;
  /** Events list only: show every event instead of just the warnings. */
  showAllEvents: boolean;
}

const DEFAULT_LIST_VIEW: K8sListView = { filter: "", sort: null, autoRefresh: false, showAllEvents: false };

/** A filter containing `=` is a label selector handled by the cluster; anything else filters names locally. */
export function labelSelectorOf(filter: string): string {
  return filter.includes("=") ? filter.trim() : "";
}

/**
 * Inner tab of the cluster page. Ids: `overview` | `list:<typeKey>` | `res:<typeKey>:<ns>:<name>` |
 * `term:<sessionKey>`.
 */
export interface K8sInnerTab {
  id: string;
  label: string;
}

/** How a Pod terminal session ended (`k8s:exec:end` payload), or why it couldn't start. */
export interface K8sTerminalEnd {
  reason: "exited" | "closed" | "error";
  exitCode?: number;
  error?: string;
  /** Why the container terminated (e.g. OOMKilled), read from the Pod status. */
  containerReason?: string;
  /** The Pod was deleted. */
  podGone?: boolean;
  /** The session never started: the cluster refused the exec. */
  startFailed?: boolean;
}

/** A Pod terminal tab (`term:<n>`). Its xterm and exec session live in podTerminalRegistry, outside React. */
export interface K8sTerminalState {
  namespace: string;
  pod: string;
  container: string;
  /** The Pod's regular containers. */
  containers: string[];
  /** 'auto' | '/bin/bash' | '/bin/sh' | a custom command. */
  shell: string;
  /** The shell the session actually runs ('auto' resolved to bash or sh); null until it reports. */
  shellUsed: string | null;
  status: "connecting" | "connected" | "ended";
  end: K8sTerminalEnd | null;
}

export type K8sTerminalSpec = Pick<K8sTerminalState, "namespace" | "pod" | "container" | "containers" | "shell">;

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

export { DEFAULT_LIST_VIEW };

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

/** Splits a `res:<typeKey>:<ns>:<name>` tab id. */
export function parseResourceTabId(id: string): { typeKey: string; namespace: string; name: string } | null {
  if (!id.startsWith("res:")) return null;
  const [, typeKey, namespace, name] = id.split(":");
  return name === undefined ? null : { typeKey, namespace, name };
}

/** Events cache key: `<namespace>|w` (warnings only) or `<namespace>|a` (all). */
export function eventsKey(namespace: string, warningOnly: boolean) {
  return `${namespace}|${warningOnly ? "w" : "a"}`;
}

/** Cache key of a sidebar count: cluster-scoped types are counted once, so their namespace is ''. */
export function countKey(typeKey: string, namespace: string) {
  return `${typeKey}|${namespace}`;
}

/** The abnormal number next to a type in the sidebar; undefined when there is none to show (not read, or failed). */
export function abnormalCountOf(health: K8sClusterHealth | undefined, typeKey: string): number | undefined {
  if (!health) return undefined;
  if (typeKey === "/events") return health.errors.events ? undefined : health.warningEvents;
  return health.abnormalCounts[typeKey];
}

const COUNT_CONCURRENCY = 6;

let terminalSeq = 0;

type Refresher = () => Promise<unknown>;

interface K8sStoreState {
  clusters: Record<number, K8sClusterState>;
  lists: Record<number, Record<string, K8sListEntry>>;
  /** Log panel state of a detail's Logs sub-tab, keyed by the detail's `res:` tab id. */
  logTabs: Record<number, Record<string, LogTabState>>;
  /** The sub-tab a detail shows (absent = overview), keyed by its `res:` tab id. */
  detailSubTabs: Record<number, Record<string, string>>;
  /** Generic resource details keyed by their `res:` inner tab id. */
  details: Record<number, Record<string, K8sDetailEntry>>;
  /** Filter / sort / auto-refresh per list tab, `<typeKey>` → view. */
  listViews: Record<number, Record<string, K8sListView>>;
  /** Pod terminal tabs keyed by their `term:` tab id; removing one ends its session. */
  terminals: Record<number, Record<string, K8sTerminalState>>;
  /** Overview / sidebar health summary per selected namespace ('' = all). */
  health: Record<number, Record<string, Loadable<K8sClusterHealth>>>;
  /** Sidebar counts keyed by `countKey`. */
  counts: Record<number, Record<string, K8sCount>>;
  /** Events lists keyed by `eventsKey`. */
  eventLists: Record<number, Record<string, Loadable<K8sEvent[]>>>;

  /** Creates the cluster's state and reads it; a cluster that already has state (page remount) is left as is. */
  initCluster: (assetId: number, defaultNamespace: string) => void;
  loadVersion: (assetId: number) => Promise<void>;
  loadTypes: (assetId: number) => Promise<void>;
  loadNamespaces: (assetId: number) => Promise<void>;
  setNamespace: (assetId: number, namespace: string) => void;
  toggleCategory: (assetId: number, key: string) => void;
  /** Reads a list unless it is cached (or `force`); pass namespace '' for cluster-scoped types and all namespaces. */
  loadList: (assetId: number, typeKey: string, namespace: string, opts?: { force?: boolean }) => Promise<void>;
  /** Updates one list tab's filter / sort / auto-refresh (the rest is kept). */
  setListView: (assetId: number, typeKey: string, patch: Partial<K8sListView>) => void;
  /** Reads a resource detail and its events unless cached (or `force`); only the manual / action paths force. */
  loadDetail: (assetId: number, tabId: string, opts?: { force?: boolean }) => Promise<void>;
  /** Reads the health summary of a namespace unless it is cached (or `force`). */
  loadHealth: (assetId: number, namespace: string, opts?: { force?: boolean }) => Promise<void>;
  /** Counts every discovered type for the selected namespace (bounded concurrency), skipping cached counts unless `force`. */
  loadCounts: (assetId: number, opts?: { force?: boolean }) => Promise<void>;
  /** Reads the events of a namespace (warnings only or all) unless cached (or `force`). */
  loadEvents: (assetId: number, namespace: string, warningOnly: boolean, opts?: { force?: boolean }) => Promise<void>;
  openTab: (assetId: number, tab: K8sInnerTab) => void;
  setActiveTab: (assetId: number, id: string) => void;
  /** Closes tabs (the overview tab never closes); `activate` picks the tab to show afterwards. */
  closeTabs: (assetId: number, ids: string[], activate?: string) => void;
  /** Re-reads version, types, namespaces, every loaded list / detail and every registered view. */
  refreshAll: (assetId: number) => Promise<void>;
  /** Lets a view take part in `refreshAll`; returns the unregister function. */
  registerRefresher: (assetId: number, key: string, fn: Refresher) => () => void;
  /** Selects a detail's sub-tab (e.g. 'logs'); the detail keeps it while its tab is open. */
  setDetailSubTab: (assetId: number, tabId: string, subTab: string) => void;
  /** Creates the log panel state of a detail's Logs sub-tab unless it exists. */
  initLogTab: (assetId: number, tabId: string) => void;
  updateLogTab: (assetId: number, tabId: string, update: LogTabStateUpdate) => void;
  /** Opens a new Pod terminal tab (every call a new one) and returns its tab id. */
  openTerminal: (assetId: number, spec: K8sTerminalSpec) => string;
  updateTerminal: (assetId: number, tabId: string, patch: Partial<K8sTerminalState>) => void;
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
    logTabs: {},
    detailSubTabs: {},
    details: {},
    listViews: {},
    terminals: {},
    health: {},
    counts: {},
    eventLists: {},

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
          withPhase: false,
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
          : { status: "loading", columns: [], rows: [], error: null, reloading: false, filterError: null }
      );
      const labelSelector = labelSelectorOf((get().listViews[assetId]?.[typeKey] ?? DEFAULT_LIST_VIEW).filter);
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
          labelSelector,
          fieldSelector: "",
          includeObject: false,
          // The Pod terminal action's state is the Pod phase, which the Status column doesn't show (CrashLoopBackOff).
          withPhase: typeKey === "/pods",
        });
        if (!isCurrent(assetId, slot, token)) return;
        const data = JSON.parse(result) as K8sResourceList;
        setEntry({
          status: "ready",
          columns: data.columns,
          rows: data.rows,
          error: null,
          reloading: false,
          filterError: null,
        });
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        // A rejected selector isn't a failed list: the rows already shown stay, the reason goes under the filter.
        if (labelSelector && prev?.status === "ready") {
          setEntry({ ...prev, reloading: false, filterError: errorMessage(e) });
          return;
        }
        setEntry({
          status: "error",
          columns: prev?.columns ?? [],
          rows: prev?.rows ?? [],
          error: errorMessage(e),
          reloading: false,
          filterError: null,
        });
      }
    },

    loadDetail: async (assetId, tabId, opts) => {
      const cluster = get().clusters[assetId];
      const ref = parseResourceTabId(tabId);
      if (!cluster || !ref) return;
      const prev = get().details[assetId]?.[tabId];
      if (prev && !opts?.force) return;
      const patchEntry = (patch: (e: K8sDetailEntry) => Partial<K8sDetailEntry>) =>
        set((s) => {
          const entry = s.details[assetId]?.[tabId];
          if (!s.clusters[assetId] || !entry) return s;
          return {
            details: { ...s.details, [assetId]: { ...s.details[assetId], [tabId]: { ...entry, ...patch(entry) } } },
          };
        });

      const slot = `detail:${tabId}`;
      const token = begin(assetId, slot);
      set((s) => ({
        details: {
          ...s.details,
          [assetId]: {
            ...s.details[assetId],
            [tabId]: {
              resource: pending(prev?.resource),
              events: prev?.events ?? { status: "loading", error: null },
            },
          },
        },
      }));
      const version = apiVersionOf(cluster, ref.typeKey);
      const [group, resource] = ref.typeKey.split("/");
      let detail: K8sResourceDetail;
      try {
        if (!version) throw new Error(`unknown resource type ${ref.typeKey}`);
        detail = JSON.parse(
          await GetK8sResource({
            assetId,
            group,
            version,
            resource,
            namespace: ref.namespace,
            name: ref.name,
          } as never)
        ) as K8sResourceDetail;
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        patchEntry((en) => ({ resource: { ...en.resource, status: "error", error: errorMessage(e) } }));
        return;
      }
      if (!isCurrent(assetId, slot, token)) return;
      patchEntry(() => ({ resource: { status: "ready", data: detail, error: null } }));
      if (detail.notFound || !detail.object) return;

      const uid = (detail.object.metadata as { uid?: string } | undefined)?.uid ?? "";
      try {
        const result = JSON.parse(
          await ListK8sEvents({ assetId, namespace: ref.namespace, involvedUid: uid, warningOnly: false } as never)
        ) as { events: K8sEvent[] };
        if (!isCurrent(assetId, slot, token)) return;
        patchEntry(() => ({ events: { status: "ready", data: result.events, error: null } }));
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        patchEntry((en) => ({ events: { ...en.events, status: "error", error: errorMessage(e) } }));
      }
    },

    setListView: (assetId, typeKey, patch) =>
      set((s) => {
        if (!s.clusters[assetId]) return s;
        const views = s.listViews[assetId] ?? {};
        const next = { ...(views[typeKey] ?? DEFAULT_LIST_VIEW), ...patch };
        return { listViews: { ...s.listViews, [assetId]: { ...views, [typeKey]: next } } };
      }),

    loadHealth: async (assetId, namespace, opts) => {
      if (!get().clusters[assetId]) return;
      const prev = get().health[assetId]?.[namespace];
      if (prev && !opts?.force) return;
      const slot = `health:${namespace}`;
      const token = begin(assetId, slot);
      const setEntry = (entry: Loadable<K8sClusterHealth>) =>
        set((s) => {
          if (!s.clusters[assetId]) return s;
          return { health: { ...s.health, [assetId]: { ...s.health[assetId], [namespace]: entry } } };
        });
      setEntry(pending(prev));
      try {
        const data = JSON.parse(await GetK8sClusterHealth(assetId, namespace)) as K8sClusterHealth;
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({ status: "ready", data, error: null });
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({ ...prev, status: "error", error: errorMessage(e) });
      }
    },

    loadCounts: async (assetId, opts) => {
      const cluster = get().clusters[assetId];
      const types = cluster?.types.data?.types;
      if (!cluster || !types) return;
      const namespace = cluster.namespace;
      const todo = types
        .map((t) => ({
          typeKey: typeKeyOf(t),
          group: t.group,
          version: t.version,
          resource: t.resource,
          ns: t.namespaced ? namespace : "",
        }))
        .filter((t) => opts?.force || !get().counts[assetId]?.[countKey(t.typeKey, t.ns)]);

      const setCount = (key: string, count: K8sCount) =>
        set((s) => {
          if (!s.clusters[assetId]) return s;
          return { counts: { ...s.counts, [assetId]: { ...s.counts[assetId], [key]: count } } };
        });

      const run = async (t: (typeof todo)[number]) => {
        const key = countKey(t.typeKey, t.ns);
        const slot = `count:${key}`;
        const token = begin(assetId, slot);
        const prev = get().counts[assetId]?.[key];
        // A count already shown stays while it is re-read.
        if (prev?.status !== "ready") setCount(key, { status: "loading", error: null });
        try {
          const result = JSON.parse(
            await CountK8sResources({
              assetId,
              group: t.group,
              version: t.version,
              resource: t.resource,
              namespace: t.ns,
              labelSelector: "",
              fieldSelector: "",
              includeObject: false,
              withPhase: false,
            })
          ) as { count: number };
          if (!isCurrent(assetId, slot, token)) return;
          setCount(key, { status: "ready", count: result.count, error: null });
        } catch (e) {
          if (!isCurrent(assetId, slot, token)) return;
          setCount(key, { status: "error", error: errorMessage(e) });
        }
      };

      const queue = [...todo];
      const worker = async () => {
        for (let t = queue.shift(); t; t = queue.shift()) await run(t);
      };
      await Promise.all(Array.from({ length: Math.min(COUNT_CONCURRENCY, queue.length) }, worker));
    },

    loadEvents: async (assetId, namespace, warningOnly, opts) => {
      if (!get().clusters[assetId]) return;
      const key = eventsKey(namespace, warningOnly);
      const prev = get().eventLists[assetId]?.[key];
      if (prev && !opts?.force) return;
      const slot = `events:${key}`;
      const token = begin(assetId, slot);
      const setEntry = (entry: Loadable<K8sEvent[]>) =>
        set((s) => {
          if (!s.clusters[assetId]) return s;
          return { eventLists: { ...s.eventLists, [assetId]: { ...s.eventLists[assetId], [key]: entry } } };
        });
      setEntry(pending(prev));
      try {
        const result = JSON.parse(await ListK8sEvents({ assetId, namespace, involvedUid: "", warningOnly })) as {
          events: K8sEvent[];
        };
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({ status: "ready", data: result.events, error: null });
      } catch (e) {
        if (!isCurrent(assetId, slot, token)) return;
        setEntry({ ...prev, status: "error", error: errorMessage(e) });
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
        const subTabs = { ...s.detailSubTabs[assetId] };
        const resDetails = { ...s.details[assetId] };
        const terminals = { ...s.terminals[assetId] };
        for (const id of closing) {
          delete logs[id];
          delete subTabs[id];
          delete terminals[id];
          // Dropped with its tab, so reopening reads it afresh and the top refresh stops re-reading it.
          delete resDetails[id];
          tokens.delete(`${assetId}|detail:${id}`);
        }
        return {
          logTabs: { ...s.logTabs, [assetId]: logs },
          detailSubTabs: { ...s.detailSubTabs, [assetId]: subTabs },
          details: { ...s.details, [assetId]: resDetails },
          terminals: { ...s.terminals, [assetId]: terminals },
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
      for (const tabId of Object.keys(state.details[assetId] ?? {})) {
        reads.push(state.loadDetail(assetId, tabId, { force: true }));
      }
      // Counts, health and events of namespaces other than the selected one are dropped (they would be stale when
      // switched back to); the selected namespace is read again.
      const namespace = state.clusters[assetId].namespace;
      const keep = <T>(entries: Record<string, T> | undefined, inScope: (key: string) => boolean) =>
        Object.fromEntries(Object.entries(entries ?? {}).filter(([key]) => inScope(key)));
      set((s) => ({
        counts: {
          ...s.counts,
          [assetId]: keep(s.counts[assetId], (k) => ["", namespace].includes(k.slice(k.lastIndexOf("|") + 1))),
        },
        health: { ...s.health, [assetId]: keep(s.health[assetId], (k) => k === namespace) },
        eventLists: {
          ...s.eventLists,
          [assetId]: keep(s.eventLists[assetId], (k) => k.slice(0, k.lastIndexOf("|")) === namespace),
        },
      }));
      reads.push(state.loadCounts(assetId, { force: true }), state.loadHealth(assetId, namespace, { force: true }));
      for (const key of Object.keys(get().eventLists[assetId] ?? {})) {
        const sep = key.lastIndexOf("|");
        reads.push(state.loadEvents(assetId, key.slice(0, sep), key.slice(sep + 1) === "w", { force: true }));
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

    setDetailSubTab: (assetId, tabId, subTab) =>
      set((s) => {
        if (!s.clusters[assetId]) return s;
        return { detailSubTabs: { ...s.detailSubTabs, [assetId]: { ...s.detailSubTabs[assetId], [tabId]: subTab } } };
      }),

    initLogTab: (assetId, tabId) =>
      set((s) => {
        if (!s.clusters[assetId] || s.logTabs[assetId]?.[tabId]) return s;
        const initial: LogTabState = {
          logStreamID: null,
          logContainer: "",
          logTailLines: 200,
          logError: null,
          logBuffers: {},
        };
        return { logTabs: { ...s.logTabs, [assetId]: { ...s.logTabs[assetId], [tabId]: initial } } };
      }),

    updateLogTab: (assetId, tabId, update) =>
      set((s) => {
        const existing = s.logTabs[assetId]?.[tabId];
        // The log panel reports its stream stopping when it unmounts, which also happens right after its
        // tab (or the whole cluster tab) closed — that state is gone and must stay gone.
        if (!existing) return s;
        const next = typeof update === "function" ? update(existing) : { ...existing, ...update };
        return { logTabs: { ...s.logTabs, [assetId]: { ...s.logTabs[assetId], [tabId]: next } } };
      }),

    openTerminal: (assetId, spec) => {
      const id = `term:${++terminalSeq}`;
      set((s) => {
        if (!s.clusters[assetId]) return s;
        const state: K8sTerminalState = { ...spec, shellUsed: null, status: "connecting", end: null };
        return { terminals: { ...s.terminals, [assetId]: { ...s.terminals[assetId], [id]: state } } };
      });
      get().openTab(assetId, { id, label: `${spec.pod} · ${spec.container}` });
      return id;
    },

    updateTerminal: (assetId, tabId, patch) =>
      set((s) => {
        const existing = s.terminals[assetId]?.[tabId];
        // A session reports back after its tab (or the cluster tab) closed; that terminal is gone for good.
        if (!existing) return s;
        const next = { ...existing, ...patch };
        const cluster = s.clusters[assetId];
        // The tab is named after the container it is attached to.
        const relabel = cluster && next.container !== existing.container;
        return {
          terminals: { ...s.terminals, [assetId]: { ...s.terminals[assetId], [tabId]: next } },
          ...(relabel && {
            clusters: {
              ...s.clusters,
              [assetId]: {
                ...cluster,
                tabs: cluster.tabs.map((t) =>
                  t.id === tabId ? { ...t, label: `${next.pod} · ${next.container}` } : t
                ),
              },
            },
          }),
        };
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
          logTabs: without(s.logTabs),
          detailSubTabs: without(s.detailSubTabs),
          details: without(s.details),
          listViews: without(s.listViews),
          terminals: without(s.terminals),
          health: without(s.health),
          counts: without(s.counts),
          eventLists: without(s.eventLists),
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
