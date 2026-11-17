import { beforeEach, describe, expect, it, vi } from "vitest";
import { GetK8sClusterVersion, GetK8sResourceTypes, ListK8sResources } from "../../../wailsjs/go/k8s/K8s";
import type { k8s } from "../../../wailsjs/go/models";
import { objectsKey, useK8sStore } from "../k8sStore";
import { useTabStore } from "../tabStore";

const TYPES = {
  types: [
    {
      group: "",
      version: "v1",
      resource: "nodes",
      kind: "Node",
      namespaced: false,
      verbs: ["list"],
      category: "cluster",
    },
    {
      group: "",
      version: "v1",
      resource: "namespaces",
      kind: "Namespace",
      namespaced: false,
      verbs: ["list"],
      category: "cluster",
    },
    {
      group: "",
      version: "v1",
      resource: "pods",
      kind: "Pod",
      namespaced: true,
      verbs: ["list"],
      category: "workloads",
    },
    {
      group: "apps",
      version: "v1",
      resource: "deployments",
      kind: "Deployment",
      namespaced: true,
      verbs: ["list"],
      category: "workloads",
    },
  ],
  failedGroups: [],
};

function table(names: string[], namespace = "") {
  return JSON.stringify({
    columns: [
      { name: "Name", type: "string", format: "name" },
      { name: "Status", type: "string", format: "" },
    ],
    rows: names.map((name) => ({
      name,
      namespace,
      uid: `uid-${name}`,
      creationTimestamp: "2026-11-16T00:00:00Z",
      labels: {},
      ownerReferences: [],
      cells: [name, "Running"],
    })),
  });
}

type ListHandler = (req: k8s.K8sListRequest) => Promise<string>;

function mockCluster(list: ListHandler) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(
    JSON.stringify({ version: "v1.31.4", platform: "linux/amd64" }) as never
  );
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(TYPES) as never);
  vi.mocked(ListK8sResources).mockImplementation(list as never);
}

const namespacesOK: ListHandler = async (req) => {
  if (req.resource === "namespaces") return table(["default", "prod", "dev"]);
  return table([`${req.resource}-${req.namespace || "all"}`], req.namespace);
};

function listCalls(resource: string) {
  return vi
    .mocked(ListK8sResources)
    .mock.calls.map(([req]) => req)
    .filter((req) => req.resource === resource);
}

async function flush() {
  for (let i = 0; i < 5; i++) await Promise.resolve();
}

let nextAsset = 1000;

describe("k8sStore", () => {
  let assetId: number;

  beforeEach(() => {
    assetId = ++nextAsset;
    vi.mocked(GetK8sClusterVersion).mockReset();
    vi.mocked(GetK8sResourceTypes).mockReset();
    vi.mocked(ListK8sResources).mockReset();
  });

  it("selects the asset's namespace by default, and all namespaces when none is configured", async () => {
    mockCluster(namespacesOK);
    useK8sStore.getState().initCluster(assetId, "prod");
    useK8sStore.getState().initCluster(assetId + 500, "");
    await flush();

    expect(useK8sStore.getState().clusters[assetId].namespace).toBe("prod");
    expect(useK8sStore.getState().clusters[assetId + 500].namespace).toBe("");
    expect(useK8sStore.getState().clusters[assetId].namespaces).toMatchObject({
      status: "ready",
      data: ["default", "prod", "dev"],
    });
  });

  it("keeps the configured namespace and reports the reason when namespaces can't be listed", async () => {
    mockCluster(async (req) => {
      if (req.resource === "namespaces") throw new Error('namespaces is forbidden: User "ro" cannot list');
      return table([]);
    });
    useK8sStore.getState().initCluster(assetId, "prod");
    await flush();

    const cluster = useK8sStore.getState().clusters[assetId];
    expect(cluster.namespace).toBe("prod");
    expect(cluster.namespaces).toMatchObject({
      status: "error",
      error: 'namespaces is forbidden: User "ro" cannot list',
    });
  });

  it("keeps state when the page initialises again after an unmount", async () => {
    mockCluster(namespacesOK);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();
    store.setNamespace(assetId, "dev");
    store.openTab(assetId, { id: "list:/pods", label: "Pod" });

    useK8sStore.getState().initCluster(assetId, "prod");
    await flush();

    const cluster = useK8sStore.getState().clusters[assetId];
    expect(cluster.namespace).toBe("dev");
    expect(cluster.activeTabId).toBe("list:/pods");
    expect(cluster.tabs.map((t) => t.id)).toEqual(["overview", "list:/pods"]);
    expect(GetK8sClusterVersion).toHaveBeenCalledTimes(1);
  });

  it("caches lists per type and namespace, so switching back does not re-read", async () => {
    mockCluster(namespacesOK);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();

    await store.loadList(assetId, "/pods", "prod");
    store.setNamespace(assetId, "dev");
    await store.loadList(assetId, "/pods", "dev");
    store.setNamespace(assetId, "prod");
    await store.loadList(assetId, "/pods", "prod");
    await store.loadList(assetId, "apps/deployments", "prod");

    expect(listCalls("pods").map((r) => r.namespace)).toEqual(["prod", "dev"]);
    expect(listCalls("deployments")[0]).toMatchObject({ group: "apps", version: "v1", namespace: "prod" });
    // Pod rows carry their phase (the terminal action's state); other types don't pay for the full objects.
    expect(listCalls("pods")[0].withPhase).toBe(true);
    expect(listCalls("deployments")[0].withPhase).toBe(false);
    const lists = useK8sStore.getState().lists[assetId];
    expect(lists["/pods|prod"].rows.map((r) => r.name)).toEqual(["pods-prod"]);
    expect(lists["/pods|dev"].rows.map((r) => r.name)).toEqual(["pods-dev"]);
  });

  it("records a failed list as an error with the cluster's reason, not as an empty list", async () => {
    mockCluster(async (req) => {
      if (req.resource === "pods") throw new Error('pods is forbidden: User "ro" cannot list resource "pods"');
      return namespacesOK(req);
    });
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();

    await store.loadList(assetId, "/pods", "prod");

    expect(useK8sStore.getState().lists[assetId]["/pods|prod"]).toMatchObject({
      status: "error",
      error: 'pods is forbidden: User "ro" cannot list resource "pods"',
    });
  });

  it("refresh re-reads only loaded lists, replacing their content (#212)", async () => {
    let generation = "old";
    mockCluster(async (req) => {
      if (req.resource === "namespaces") return table(["default", "prod"]);
      return table([`${req.resource}-${generation}`], req.namespace);
    });
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();
    await store.loadList(assetId, "/pods", "prod");

    generation = "new";
    await store.refreshAll(assetId);

    expect(listCalls("pods").map((r) => r.namespace)).toEqual(["prod", "prod"]);
    expect(listCalls("deployments")).toEqual([]);
    expect(useK8sStore.getState().lists[assetId]["/pods|prod"].rows.map((r) => r.name)).toEqual(["pods-new"]);
    expect(GetK8sClusterVersion).toHaveBeenCalledTimes(2);
    expect(GetK8sResourceTypes).toHaveBeenCalledTimes(2);
    expect(listCalls("namespaces")).toHaveLength(2);
    expect(useK8sStore.getState().clusters[assetId].refreshing).toBe(false);
  });

  it("re-reads a cached list when the label selector differs from the one it was read with", async () => {
    mockCluster(namespacesOK);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();
    await store.loadList(assetId, "/pods", "prod");
    store.setNamespace(assetId, "dev");
    store.setListView(assetId, "/pods", { filter: "app=api" });
    await store.loadList(assetId, "/pods", "dev");

    store.setNamespace(assetId, "prod");
    await store.loadList(assetId, "/pods", "prod");
    // A name filter is applied locally, so it never re-reads.
    store.setListView(assetId, "/pods", { filter: "api" });
    await store.loadList(assetId, "/pods", "dev");
    await store.loadList(assetId, "/pods", "dev");

    expect(listCalls("pods").map((r) => [r.namespace, r.labelSelector])).toEqual([
      ["prod", ""],
      ["dev", "app=api"],
      ["prod", "app=api"],
      ["dev", ""],
    ]);
  });

  it("shows a failed re-read under an applied selector as a list error; only a newly rejected selector goes under the filter", async () => {
    let fail = false;
    mockCluster(async (req) => {
      if (req.resource !== "pods") return namespacesOK(req);
      if (req.labelSelector === "app=api,=bad")
        throw new Error("unable to parse requirement: found '=', expected: identifier");
      if (fail) throw new Error("dial tcp 10.0.0.1:6443: connect: connection refused");
      return table(["api"], req.namespace);
    });
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();
    store.setListView(assetId, "/pods", { filter: "app=api" });
    await store.loadList(assetId, "/pods", "prod");

    store.setListView(assetId, "/pods", { filter: "app=api,=bad" });
    await store.loadList(assetId, "/pods", "prod", { force: true });
    expect(useK8sStore.getState().lists[assetId]["/pods|prod"]).toMatchObject({
      status: "ready",
      filterError: "unable to parse requirement: found '=', expected: identifier",
    });
    expect(useK8sStore.getState().lists[assetId]["/pods|prod"].rows.map((r) => r.name)).toEqual(["api"]);

    store.setListView(assetId, "/pods", { filter: "app=api" });
    fail = true;
    await store.loadList(assetId, "/pods", "prod", { force: true });
    expect(useK8sStore.getState().lists[assetId]["/pods|prod"]).toMatchObject({
      status: "error",
      error: "dial tcp 10.0.0.1:6443: connect: connection refused",
      filterError: null,
    });
  });

  it("caches full-object lists per request and re-reads the loaded ones on refresh", async () => {
    let image = "old";
    mockCluster(async (req) => {
      if (req.resource !== "pods") return namespacesOK(req);
      const rows = [{ name: "api-1", object: { metadata: { name: "api-1" }, spec: { image } } }];
      return JSON.stringify({ columns: [], rows });
    });
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();
    const req = { group: "", version: "v1", resource: "pods", namespace: "prod", labelSelector: "app=api" };

    await store.loadObjects(assetId, req);
    await store.loadObjects(assetId, req);
    expect(listCalls("pods")).toHaveLength(1);
    expect(listCalls("pods")[0]).toMatchObject({ includeObject: true, labelSelector: "app=api" });

    image = "new";
    await store.refreshAll(assetId);
    expect(listCalls("pods")).toHaveLength(2);
    expect(useK8sStore.getState().objectLists[assetId][objectsKey(req)].data).toEqual([
      { name: "api-1", object: { metadata: { name: "api-1" }, spec: { image: "new" } } },
    ]);
  });

  it("keeps the overview tab when closing tabs and activates a neighbour of the closed active tab", () => {
    mockCluster(namespacesOK);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "");
    store.openTab(assetId, { id: "list:/pods", label: "Pod" });
    store.openTab(assetId, { id: "list:apps/deployments", label: "Deployment" });
    store.openTab(assetId, { id: "list:/pods", label: "Pod" });

    store.closeTabs(assetId, ["overview", "list:/pods"]);

    const cluster = useK8sStore.getState().clusters[assetId];
    expect(cluster.tabs.map((t) => t.id)).toEqual(["overview", "list:apps/deployments"]);
    expect(cluster.activeTabId).toBe("list:apps/deployments");
  });

  it("drops a detail's sub-tab choice and log state with its tab, so reopening starts on the overview", () => {
    mockCluster(namespacesOK);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    const tab = { id: "res:/pods:prod:api", label: "api" };
    store.openTab(assetId, tab);
    store.setDetailSubTab(assetId, tab.id, "logs");
    store.initLogTab(assetId, tab.id);
    expect(useK8sStore.getState().detailSubTabs[assetId][tab.id]).toBe("logs");
    expect(useK8sStore.getState().logTabs[assetId][tab.id]).toBeDefined();

    store.closeTabs(assetId, [tab.id]);

    expect(useK8sStore.getState().detailSubTabs[assetId][tab.id]).toBeUndefined();
    expect(useK8sStore.getState().logTabs[assetId][tab.id]).toBeUndefined();
  });

  it("keeps collapsed sidebar categories for the app session, across closing and reopening the cluster tab", () => {
    mockCluster(namespacesOK);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    store.toggleCategory(assetId, "rbac");
    store.toggleCategory(assetId, "storage");
    store.toggleCategory(assetId, "storage");

    store.clearCluster(assetId);
    store.initCluster(assetId, "prod");

    expect(useK8sStore.getState().collapsedCategories[assetId]).toEqual(["rbac"]);
  });

  it("clears a cluster's state when its cluster tab closes", async () => {
    mockCluster(namespacesOK);
    const other = assetId + 500;
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    store.initCluster(other, "prod");
    await flush();
    await store.loadList(assetId, "/pods", "prod");
    useTabStore.getState().openTab({
      id: `k8s-${assetId}`,
      type: "page",
      label: "k8s",
      meta: { type: "page", pageId: "k8s-cluster", assetId },
    });

    useTabStore.getState().closeTab(`k8s-${assetId}`);

    const state = useK8sStore.getState();
    expect(state.clusters[assetId]).toBeUndefined();
    expect(state.lists[assetId]).toBeUndefined();
    expect(state.clusters[other]).toBeDefined();
  });
});
