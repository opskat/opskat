import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  GetK8sClusterVersion,
  GetK8sResourceTypes,
  ListK8sResources,
  GetK8sPodDetail,
} from "../../../wailsjs/go/k8s/K8s";
import type { k8s } from "../../../wailsjs/go/models";
import { useK8sStore } from "../k8sStore";
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
    vi.mocked(GetK8sPodDetail).mockReset();
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

  it("refresh re-reads only loaded lists and registered views, replacing their content (#212)", async () => {
    let generation = "old";
    mockCluster(async (req) => {
      if (req.resource === "namespaces") return table(["default", "prod"]);
      return table([`${req.resource}-${generation}`], req.namespace);
    });
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    await flush();
    await store.loadList(assetId, "/pods", "prod");
    const refresher = vi.fn().mockResolvedValue(undefined);
    const removed = vi.fn().mockResolvedValue(undefined);
    store.registerRefresher(assetId, "overview", refresher);
    store.registerRefresher(assetId, "gone", removed)();

    generation = "new";
    await store.refreshAll(assetId);

    expect(listCalls("pods").map((r) => r.namespace)).toEqual(["prod", "prod"]);
    expect(listCalls("deployments")).toEqual([]);
    expect(useK8sStore.getState().lists[assetId]["/pods|prod"].rows.map((r) => r.name)).toEqual(["pods-new"]);
    expect(GetK8sClusterVersion).toHaveBeenCalledTimes(2);
    expect(GetK8sResourceTypes).toHaveBeenCalledTimes(2);
    expect(listCalls("namespaces")).toHaveLength(2);
    expect(refresher).toHaveBeenCalledTimes(1);
    expect(removed).not.toHaveBeenCalled();
    expect(useK8sStore.getState().clusters[assetId].refreshing).toBe(false);
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

  it("forgets a pod detail once no open tab shows it, so reopening reads it again", async () => {
    mockCluster(namespacesOK);
    vi.mocked(GetK8sPodDetail).mockResolvedValue(JSON.stringify({ name: "api", containers: [] }) as never);
    const store = useK8sStore.getState();
    store.initCluster(assetId, "prod");
    const detailTab = { id: "res:/pods:prod:api", label: "api" };
    store.openTab(assetId, detailTab);
    await store.loadPodDetail(assetId, "prod", "api");
    store.openPodLogs(assetId, "prod", "api", "app", "logs");

    store.closeTabs(assetId, [detailTab.id]);
    await store.loadPodDetail(assetId, "prod", "api");
    expect(GetK8sPodDetail).toHaveBeenCalledTimes(1);

    store.closeTabs(assetId, ["log:prod:api"]);
    store.openTab(assetId, detailTab);
    await store.loadPodDetail(assetId, "prod", "api");
    expect(GetK8sPodDetail).toHaveBeenCalledTimes(2);
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
