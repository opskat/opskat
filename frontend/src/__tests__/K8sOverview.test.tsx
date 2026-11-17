import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { useK8sStore } from "@/stores/k8sStore";
import {
  CountK8sResources,
  GetK8sClusterHealth,
  GetK8sClusterVersion,
  GetK8sResourceTypes,
  ListK8sEvents,
  ListK8sResources,
} from "../../wailsjs/go/k8s/K8s";
import type { asset_entity, k8s } from "../../wailsjs/go/models";

vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

const type = (group: string, resource: string, kind: string, namespaced: boolean, category: string) => ({
  group,
  version: "v1",
  resource,
  kind,
  namespaced,
  verbs: ["list"],
  category,
});
const TYPES = {
  types: [
    type("", "nodes", "Node", false, "cluster"),
    type("", "namespaces", "Namespace", false, "cluster"),
    type("", "events", "Event", true, "cluster"),
    type("", "pods", "Pod", true, "workloads"),
    type("apps", "deployments", "Deployment", true, "workloads"),
  ],
  failedGroups: [],
};

const HEALTH = {
  nodes: { total: 4, schedulable: 3, unschedulable: 1, notReady: 2 },
  pods: { total: 23, running: 20, pending: 1, abnormal: 2 },
  attention: [
    {
      group: "apps",
      version: "v1",
      resource: "deployments",
      kind: "Deployment",
      namespace: "prod",
      name: "worker",
      status: "0/1",
      reason: "MinimumReplicasUnavailable",
    },
  ],
  abnormalCounts: { "/pods": 2, "apps/deployments": 1, "/nodes": 3 },
  warningEvents: 6,
  errors: {},
};

function nodeTable() {
  return JSON.stringify({
    columns: [
      { name: "Name", type: "string", format: "name" },
      { name: "Status", type: "string", format: "" },
      { name: "Roles", type: "string", format: "" },
      { name: "Age", type: "string", format: "" },
      { name: "Version", type: "string", format: "" },
    ],
    rows: [
      ["worker-01", "Ready", "<none>", "v1.31.4"],
      ["cp-01", "Ready,SchedulingDisabled", "control-plane", "v1.31.4"],
    ].map(([name, status, roles, version]) => ({
      name,
      namespace: "",
      uid: name,
      creationTimestamp: "2026-11-01T00:00:00Z",
      labels: {},
      ownerReferences: [],
      cells: [name, status, roles, "15d", version],
    })),
  });
}

const event = (n: number) => ({
  type: "Warning",
  reason: `Reason${n}`,
  message: `message ${n}`,
  count: n,
  lastTimestamp: `2026-11-1${9 - n}T08:00:00Z`,
  involvedObject: { apiVersion: "v1", kind: "Pod", namespace: "default", name: `pod-${n}`, uid: `u${n}` },
});

let nextId = 93000;
const asset = (ID: number) =>
  ({ ID, Name: "k8s", Type: "k8s", Config: '{"namespace":"default"}' }) as asset_entity.Asset;

interface Overrides {
  health?: () => Promise<string>;
  count?: (req: k8s.K8sListRequest) => Promise<string>;
  nodes?: () => Promise<string>;
  events?: () => Promise<string>;
}

function mockCluster(o: Overrides = {}) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(
    JSON.stringify({ version: "v1.31.4", platform: "linux/amd64" }) as never
  );
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(TYPES) as never);
  vi.mocked(ListK8sResources).mockImplementation((async (req: k8s.K8sListRequest) => {
    if (req.resource === "nodes") return (o.nodes ?? (async () => nodeTable()))();
    return JSON.stringify({ columns: [{ name: "Name", type: "string", format: "name" }], rows: [] });
  }) as never);
  vi.mocked(CountK8sResources).mockImplementation(((req: k8s.K8sListRequest) =>
    (o.count ?? (async () => JSON.stringify({ count: 7 })))(req)) as never);
  vi.mocked(GetK8sClusterHealth).mockImplementation((async () =>
    (o.health ?? (async () => JSON.stringify(HEALTH)))()) as never);
  vi.mocked(ListK8sEvents).mockImplementation((async () =>
    (o.events ?? (async () => JSON.stringify({ events: [1, 2, 3, 4, 5, 6].map(event) })))()) as never);
}

const sidebar = () => screen.findByTestId("k8s-sidebar");

describe("K8s sidebar counts", () => {
  beforeEach(() => {
    for (const fn of [
      GetK8sClusterVersion,
      GetK8sResourceTypes,
      ListK8sResources,
      CountK8sResources,
      GetK8sClusterHealth,
      ListK8sEvents,
    ]) {
      vi.mocked(fn).mockReset();
    }
  });

  it("shows counts for the selected scope plus abnormal counts, and — with the reason when a count fails", async () => {
    mockCluster({
      count: async (req) => {
        if (req.resource === "deployments") throw new Error('deployments.apps is forbidden: User "u" cannot list');
        return JSON.stringify({ count: req.resource === "pods" ? 23 : 4 });
      },
    });
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const nav = within(await sidebar());

    const pods = await nav.findByTestId("k8s-type-/pods");
    await waitFor(() => expect(within(pods).getByTestId("k8s-count").textContent).toBe("23"));
    expect(within(pods).getByTestId("k8s-abnormal").textContent).toBe("2");

    const deployments = nav.getByTestId("k8s-type-apps/deployments");
    await waitFor(() => expect(within(deployments).getByTestId("k8s-count").textContent).toBe("—"));
    expect(within(deployments).getByTestId("k8s-count").getAttribute("title")).toContain("forbidden");
    expect(screen.queryByRole("alert")).toBeNull();

    // events: the abnormal number is the warning-event count; nodes' abnormal comes from the health summary
    const events = nav.getByTestId("k8s-type-/events");
    await waitFor(() => expect(within(events).getByTestId("k8s-abnormal").textContent).toBe("6"));
    expect(within(nav.getByTestId("k8s-type-/nodes")).getByTestId("k8s-abnormal").textContent).toBe("3");

    const reqs = vi.mocked(CountK8sResources).mock.calls.map(([r]) => r);
    expect(reqs.find((r) => r.resource === "pods")?.namespace).toBe("default");
    expect(reqs.find((r) => r.resource === "nodes")?.namespace).toBe("");
  });

  it("re-reads counts, health and the overview when the sidebar refresh is pressed", async () => {
    mockCluster();
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const nav = within(await sidebar());
    await waitFor(() =>
      expect(within(nav.getByTestId("k8s-type-/pods")).getByTestId("k8s-count").textContent).toBe("7")
    );
    const counts = vi.mocked(CountK8sResources).mock.calls.length;
    const healths = vi.mocked(GetK8sClusterHealth).mock.calls.length;

    await user.click(nav.getByRole("button", { name: "query.refreshTree" }));
    await waitFor(() => expect(vi.mocked(CountK8sResources).mock.calls.length).toBe(counts * 2));
    expect(vi.mocked(GetK8sClusterHealth).mock.calls.length).toBe(healths + 1);
  });

  it("counts again for another namespace and keeps each namespace's counts cached", async () => {
    mockCluster({ count: async (req) => JSON.stringify({ count: req.namespace === "prod" ? 99 : 7 }) });
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const nav = within(await sidebar());
    const podCount = () => within(nav.getByTestId("k8s-type-/pods")).getByTestId("k8s-count").textContent;
    await waitFor(() => expect(podCount()).toBe("7"));

    const id = Number(
      Object.keys(useK8sStore.getState().clusters)[Object.keys(useK8sStore.getState().clusters).length - 1]
    );
    useK8sStore.getState().setNamespace(id, "prod");
    await waitFor(() => expect(podCount()).toBe("99"));
    useK8sStore.getState().setNamespace(id, "default");
    await waitFor(() => expect(podCount()).toBe("7"));
    const podCalls = vi.mocked(CountK8sResources).mock.calls.filter(([r]) => r.resource === "pods");
    expect(podCalls.map(([r]) => r.namespace)).toEqual(["default", "prod"]);
  });
});

describe("K8s cluster overview", () => {
  beforeEach(() => {
    for (const fn of [
      GetK8sClusterVersion,
      GetK8sResourceTypes,
      ListK8sResources,
      CountK8sResources,
      GetK8sClusterHealth,
      ListK8sEvents,
    ]) {
      vi.mocked(fn).mockReset();
    }
  });

  it("renders stat cards, attention rows, at most 5 warning events and node cards", async () => {
    mockCluster();
    render(<K8sClusterPage asset={asset(nextId++)} />);

    const nodes = await screen.findByTestId("k8s-stat-nodes");
    await waitFor(() => expect(within(nodes).getByTestId("k8s-stat-nodes-total").textContent).toBe("4"));
    expect(within(nodes).getByTestId("k8s-stat-nodes-schedulable").textContent).toBe("3");
    expect(within(nodes).getByTestId("k8s-stat-nodes-unschedulable").textContent).toBe("1");
    expect(within(nodes).getByTestId("k8s-stat-nodes-notready").textContent).toBe("2");
    const pods = screen.getByTestId("k8s-stat-pods");
    expect(within(pods).getByTestId("k8s-stat-pods-total").textContent).toBe("23");
    expect(within(pods).getByTestId("k8s-stat-pods-running").textContent).toBe("20");
    expect(within(pods).getByTestId("k8s-stat-pods-pending").textContent).toBe("1");
    expect(within(pods).getByTestId("k8s-stat-pods-abnormal").textContent).toBe("2");
    expect(within(screen.getByTestId("k8s-stat-version")).getByText("v1.31.4")).toBeTruthy();

    const rows = await screen.findAllByTestId("k8s-attention-row");
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("prod/worker");
    expect(rows[0].textContent).toContain("MinimumReplicasUnavailable");

    const recent = await screen.findAllByTestId("k8s-event-row");
    expect(recent).toHaveLength(5);
    expect(vi.mocked(ListK8sEvents).mock.calls[0][0]).toMatchObject({ namespace: "default", warningOnly: true });

    expect(await screen.findAllByTestId("k8s-node-card")).toHaveLength(2);
  });

  it("opens the resource detail from an attention row and a node card, and the events list from the link", async () => {
    mockCluster();
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await user.click((await screen.findAllByTestId("k8s-attention-row"))[0]);
    expect(
      useK8sStore.getState().clusters[Object.keys(useK8sStore.getState().clusters).map(Number).pop()!].activeTabId
    ).toBe("res:apps/deployments:prod:worker");
  });

  it("shows 一切正常 when nothing needs attention", async () => {
    mockCluster({
      health: async () => JSON.stringify({ ...HEALTH, attention: [], abnormalCounts: {} }),
    });
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const block = await screen.findByTestId("k8s-attention");
    await waitFor(() => expect(within(block).getByTestId("async-state-empty").textContent).toBe("asset.k8sAllHealthy"));
  });

  it("does not claim all healthy when the health read failed", async () => {
    mockCluster({ health: async () => Promise.reject(new Error("boom health")) });
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const block = await screen.findByTestId("k8s-attention");
    await waitFor(() => expect(within(block).getByTestId("async-state-error")).toBeTruthy());
    expect(within(block).queryByTestId("async-state-empty")).toBeNull();
  });

  it("keeps a block's last numbers and adds the reason and a retry when its refresh fails", async () => {
    let fail = false;
    mockCluster({
      health: async () => {
        if (fail) throw new Error("dial tcp 10.0.0.1:6443: i/o timeout");
        return JSON.stringify(HEALTH);
      },
      events: async () => {
        if (fail) throw new Error("events: i/o timeout");
        return JSON.stringify({ events: [event(1)] });
      },
    });
    const user = userEvent.setup();
    const id = nextId++;
    render(<K8sClusterPage asset={asset(id)} />);
    const pods = await screen.findByTestId("k8s-stat-pods");
    await waitFor(() => expect(within(pods).getByTestId("k8s-stat-pods-total").textContent).toBe("23"));
    const recent = screen.getByTestId("k8s-recent-events");
    await waitFor(() => expect(within(recent).getAllByTestId("k8s-event-row")).toHaveLength(1));

    fail = true;
    await useK8sStore.getState().refreshAll(id);

    await waitFor(() =>
      expect(within(pods).getByTestId("k8s-stale-error").textContent).toContain("dial tcp 10.0.0.1:6443: i/o timeout")
    );
    expect(within(pods).getByTestId("k8s-stat-pods-total").textContent).toBe("23");
    expect(within(screen.getByTestId("k8s-attention")).getByTestId("k8s-stale-error")).toBeTruthy();
    expect(within(recent).getByTestId("k8s-stale-error").textContent).toContain("events: i/o timeout");
    expect(within(recent).getAllByTestId("k8s-event-row")).toHaveLength(1);

    fail = false;
    const calls = vi.mocked(GetK8sClusterHealth).mock.calls.length;
    await user.click(within(pods).getByTestId("k8s-stale-retry"));
    await waitFor(() => expect(within(pods).queryByTestId("k8s-stale-error")).toBeNull());
    expect(vi.mocked(GetK8sClusterHealth).mock.calls.length).toBe(calls + 1);
  });

  it("offers a retry when only some 需要关注 sections failed", async () => {
    mockCluster({
      health: async () => JSON.stringify({ ...HEALTH, errors: { statefulsets: "statefulsets.apps is forbidden" } }),
    });
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const block = await screen.findByTestId("k8s-attention");
    await waitFor(() => expect(within(block).getAllByTestId("k8s-attention-row")).toHaveLength(1));
    expect(block.textContent).toContain("statefulsets.apps is forbidden");
    const calls = vi.mocked(GetK8sClusterHealth).mock.calls.length;
    await userEvent.setup().click(within(block).getByRole("button", { name: "action.retry" }));
    await waitFor(() => expect(vi.mocked(GetK8sClusterHealth).mock.calls.length).toBe(calls + 1));
  });

  it("each block fails and retries on its own", async () => {
    let nodesFail = true;
    let eventsFail = true;
    mockCluster({
      health: async () =>
        JSON.stringify({
          ...HEALTH,
          nodes: { total: 0, schedulable: 0, unschedulable: 0, notReady: 0, error: "nodes is forbidden" },
          errors: { nodes: "nodes is forbidden" },
        }),
      nodes: async () => {
        if (nodesFail) throw new Error("nodes list forbidden");
        return nodeTable();
      },
      events: async () => {
        if (eventsFail) throw new Error("events forbidden");
        return JSON.stringify({ events: [event(1)] });
      },
    });
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);

    // nodes stat card, node cards and recent events fail; version, pods and attention stay
    const nodesStat = await screen.findByTestId("k8s-stat-nodes");
    await waitFor(() =>
      expect(within(nodesStat).getByTestId("async-state-error").textContent).toContain("nodes is forbidden")
    );
    const nodeCards = screen.getByTestId("k8s-node-cards");
    await waitFor(() =>
      expect(within(nodeCards).getByTestId("async-state-error").textContent).toContain("nodes list forbidden")
    );
    const recent = screen.getByTestId("k8s-recent-events");
    await waitFor(() =>
      expect(within(recent).getByTestId("async-state-error").textContent).toContain("events forbidden")
    );
    expect(within(screen.getByTestId("k8s-stat-pods")).getByTestId("k8s-stat-pods-total").textContent).toBe("23");
    expect(await screen.findAllByTestId("k8s-attention-row")).toHaveLength(1);

    // retrying events re-reads only the events
    const healthCalls = vi.mocked(GetK8sClusterHealth).mock.calls.length;
    const nodeCalls = vi.mocked(ListK8sResources).mock.calls.filter(([r]) => r.resource === "nodes").length;
    eventsFail = false;
    await user.click(within(recent).getByTestId("async-state-retry"));
    await waitFor(() => expect(within(recent).getAllByTestId("k8s-event-row")).toHaveLength(1));
    expect(vi.mocked(GetK8sClusterHealth).mock.calls.length).toBe(healthCalls);
    expect(vi.mocked(ListK8sResources).mock.calls.filter(([r]) => r.resource === "nodes").length).toBe(nodeCalls);
    expect(within(nodeCards).getByTestId("async-state-error")).toBeTruthy();

    // retrying the node cards re-reads only the nodes
    nodesFail = false;
    await user.click(within(nodeCards).getByTestId("async-state-retry"));
    await waitFor(() => expect(within(nodeCards).getAllByTestId("k8s-node-card")).toHaveLength(2));
    expect(vi.mocked(GetK8sClusterHealth).mock.calls.length).toBe(healthCalls);
  });
});
