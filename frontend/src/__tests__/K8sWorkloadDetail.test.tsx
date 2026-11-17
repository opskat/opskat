import { useImperativeHandle, type Ref } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { getK8sActions } from "@/components/k8s/actions";
import { selectorString } from "@/components/k8s/detail/workloads/selector";
import { useK8sStore, type K8sResourceRow } from "@/stores/k8sStore";
import {
  GetK8sClusterVersion,
  GetK8sResource,
  GetK8sResourceTypes,
  ListK8sEvents,
  ListK8sResources,
  StartK8sPodLogs,
} from "../../wailsjs/go/k8s/K8s";
import type { asset_entity, k8s } from "../../wailsjs/go/models";

// The xterm side of the terminal is covered by K8sPodTerminal.test; here only the entry points matter.
const startPodTerminal = vi.hoisted(() => vi.fn());
vi.mock("@/components/k8s/terminal/podTerminalRegistry", () => ({
  startPodTerminal,
  getPodTerminal: vi.fn(),
  fitPodTerminal: vi.fn(),
}));

vi.mock("@/components/k8s/K8sLogTerminal", () => ({
  K8sLogTerminal: function MockK8sLogTerminal({ ref }: { ref?: Ref<unknown> }) {
    useImperativeHandle(ref, () => ({ clear: vi.fn(), write: vi.fn() }));
    return <div data-testid="k8s-log-terminal" />;
  },
}));

const type = (group: string, version: string, resource: string, kind: string) => ({
  group,
  version,
  resource,
  kind,
  namespaced: true,
  verbs: ["list"],
  category: "workloads",
});
const types = {
  types: [
    type("", "v1", "pods", "Pod"),
    type("apps", "v1", "deployments", "Deployment"),
    type("apps", "v1", "statefulsets", "StatefulSet"),
    type("apps", "v1", "daemonsets", "DaemonSet"),
    type("apps", "v1", "replicasets", "ReplicaSet"),
    type("batch", "v1", "jobs", "Job"),
    type("batch", "v1", "cronjobs", "CronJob"),
    type("autoscaling", "v2", "horizontalpodautoscalers", "HorizontalPodAutoscaler"),
  ],
  failedGroups: [],
};

const meta = (name: string, extra: Record<string, unknown> = {}) => ({
  name,
  namespace: "default",
  uid: `uid-${name}`,
  creationTimestamp: "2026-11-10T08:00:00Z",
  labels: { app: "web" },
  ...extra,
});
const container = (name: string, image: string) => ({
  name,
  image,
  ports: [{ containerPort: 8080, name: "http", protocol: "TCP" }],
  resources: { requests: { cpu: "100m", memory: "128Mi" }, limits: { cpu: "1", memory: "512Mi" } },
});
const template = {
  spec: { containers: [container("app", "example/web:1.0"), container("sidecar", "example/proxy:2.0")] },
};
const selector = {
  matchLabels: { app: "web" },
  matchExpressions: [{ key: "tier", operator: "In", values: ["a", "b"] }],
};

const deployment = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  metadata: meta("web"),
  spec: {
    replicas: 3,
    selector,
    strategy: { type: "RollingUpdate", rollingUpdate: { maxSurge: "25%", maxUnavailable: "25%" } },
    template,
  },
  status: { replicas: 3, readyReplicas: 2, updatedReplicas: 3, availableReplicas: 2 },
};
const statefulSet = {
  apiVersion: "apps/v1",
  kind: "StatefulSet",
  metadata: meta("db"),
  spec: { replicas: 2, selector, serviceName: "db-headless", updateStrategy: { type: "RollingUpdate" }, template },
  status: { replicas: 2, readyReplicas: 2 },
};
const daemonSet = {
  apiVersion: "apps/v1",
  kind: "DaemonSet",
  metadata: meta("agent"),
  spec: {
    selector,
    updateStrategy: { type: "OnDelete" },
    template: { spec: { ...template.spec, nodeSelector: { disk: "ssd" } } },
  },
  status: {
    desiredNumberScheduled: 4,
    numberReady: 3,
    currentNumberScheduled: 4,
    updatedNumberScheduled: 4,
    numberAvailable: 3,
  },
};
const replicaSet = {
  apiVersion: "apps/v1",
  kind: "ReplicaSet",
  metadata: meta("web-abc"),
  spec: { replicas: 3, selector, template },
  status: { replicas: 3, readyReplicas: 3 },
};
const job = {
  apiVersion: "batch/v1",
  kind: "Job",
  metadata: meta("migrate"),
  spec: { completions: 5, parallelism: 2, backoffLimit: 6, selector, template },
  status: { succeeded: 4, active: 1, failed: 0 },
};
const cronJob = {
  apiVersion: "batch/v1",
  kind: "CronJob",
  metadata: meta("nightly"),
  spec: {
    schedule: "*/5 * * * *",
    suspend: true,
    concurrencyPolicy: "Forbid",
    jobTemplate: { spec: { template } },
  },
  status: { lastScheduleTime: "2026-11-16T01:00:00Z", active: [] },
};
const pod = (name: string, extra: Record<string, unknown> = {}) => ({
  apiVersion: "v1",
  kind: "Pod",
  metadata: meta(name, {
    ownerReferences: [{ apiVersion: "apps/v1", kind: "ReplicaSet", name: "web-abc", controller: true }],
  }),
  spec: {
    nodeName: "node-1",
    containers: [container("app", "example/web:1.0"), container("sidecar", "example/proxy:2.0")],
  },
  status: {
    phase: "Running",
    podIP: "10.42.0.7",
    hostIP: "192.0.2.5",
    qosClass: "Burstable",
    conditions: [{ type: "Ready", status: "True", reason: "AllReady" }],
    containerStatuses: [
      { name: "app", ready: true, restartCount: 0, state: { running: {} } },
      { name: "sidecar", ready: false, restartCount: 3, state: { waiting: { reason: "CrashLoopBackOff" } } },
    ],
  },
  ...extra,
});
const hpa = (name: string, target: string, kind = "Deployment") => ({
  kind: "HorizontalPodAutoscaler",
  metadata: meta(name),
  spec: { scaleTargetRef: { apiVersion: "apps/v1", kind, name: target }, minReplicas: 2, maxReplicas: 8 },
});
const ownedJob = (name: string, owner: string) => ({
  kind: "Job",
  metadata: meta(name, {
    ownerReferences: [{ apiVersion: "batch/v1", kind: "CronJob", name: owner, controller: true }],
  }),
  status: { succeeded: 1 },
});

const objects: Record<string, unknown> = {
  web: deployment,
  db: statefulSet,
  agent: daemonSet,
  "web-abc": replicaSet,
  migrate: job,
  nightly: cronJob,
  "web-1": pod("web-1"),
  "web-2": pod("web-2"),
};
const row = (o: { metadata: { name: string } }) => ({
  name: o.metadata.name,
  namespace: "default",
  uid: "u",
  creationTimestamp: "",
  labels: {},
  ownerReferences: [],
  cells: [],
  object: o,
});

let nextId = 95000;
const asset = (ID: number) => ({ ID, Name: "k8s", Type: "k8s", Config: "{}" }) as asset_entity.Asset;

function mockCluster(extra: { jobs?: unknown[] } = {}) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(JSON.stringify({ version: "v1", platform: "x" }) as never);
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(types) as never);
  vi.mocked(ListK8sResources).mockReset();
  vi.mocked(ListK8sResources).mockImplementation((async (req: k8s.K8sListRequest) => {
    let items: { metadata: { name: string } }[] = [];
    if (req.resource === "pods") items = [objects["web-1"], objects["web-2"]] as never;
    if (req.resource === "horizontalpodautoscalers")
      items = [hpa("other-hpa", "other"), hpa("web-hpa", "web"), hpa("rs-hpa", "web-abc", "ReplicaSet")];
    if (req.resource === "jobs") items = (extra.jobs ?? []) as never;
    return JSON.stringify({ columns: [], rows: items.map(row) });
  }) as never);
  vi.mocked(GetK8sResource).mockImplementation((async (ref: { name: string }) =>
    JSON.stringify({ notFound: false, object: objects[ref.name], yaml: "kind: x\n" })) as never);
  vi.mocked(ListK8sEvents).mockResolvedValue(JSON.stringify({ events: [] }) as never);
  vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
}

async function openDetail(typeKey: string, name: string) {
  const id = nextId++;
  mockCluster();
  render(<K8sClusterPage asset={asset(id)} />);
  await screen.findByTestId("k8s-sidebar");
  useK8sStore.getState().openTab(id, { id: `res:${typeKey}:default:${name}`, label: name });
  return id;
}

describe("selectorString", () => {
  it("turns matchLabels and matchExpressions into selector syntax and nothing into ''", () => {
    expect(selectorString(selector)).toBe("app=web,tier in (a,b)");
    expect(
      selectorString({
        matchExpressions: [
          { key: "x", operator: "NotIn", values: ["1"] },
          { key: "y", operator: "Exists" },
          { key: "z", operator: "DoesNotExist" },
        ],
      })
    ).toBe("x notin (1),y,!z");
    expect(selectorString(undefined)).toBe("");
    expect(selectorString({})).toBe("");
  });
});

describe("workload overviews", () => {
  beforeEach(() => {
    vi.mocked(GetK8sResource).mockReset();
  });

  it("Deployment: replicas, strategy, selector, HPA, containers with images / ports / resources", async () => {
    await openDetail("apps/deployments", "web");
    const ov = within(await screen.findByTestId("k8s-overview"));
    expect(await ov.findByText("2/3")).toBeInTheDocument();
    expect(ov.getByText("RollingUpdate (25% / 25%)")).toBeInTheDocument();
    expect(ov.getByText("app=web,tier in (a,b)")).toBeInTheDocument();
    expect(await ov.findByText("web-hpa · 2–8")).toBeInTheDocument();
    expect(ov.queryByText(/other-hpa/)).not.toBeInTheDocument();
    expect(ov.getByText("example/web:1.0")).toBeInTheDocument();
    expect(ov.getByText("example/proxy:2.0")).toBeInTheDocument();
    expect(ov.getAllByText("8080/http").length).toBe(2);
    expect(ov.getAllByText("100m / 128Mi").length).toBe(2);
    expect(ov.getAllByText("1 / 512Mi").length).toBe(2);
    expect(vi.mocked(ListK8sResources)).toHaveBeenCalledWith(
      expect.objectContaining({
        group: "autoscaling",
        version: "v2",
        resource: "horizontalpodautoscalers",
        includeObject: true,
      })
    );
  });

  it("StatefulSet: replicas, service name, update strategy", async () => {
    await openDetail("apps/statefulsets", "db");
    const ov = within(await screen.findByTestId("k8s-overview"));
    expect(await ov.findByText("2/2")).toBeInTheDocument();
    expect(ov.getByText("db-headless")).toBeInTheDocument();
    expect(ov.getByText("RollingUpdate")).toBeInTheDocument();
  });

  it("DaemonSet: scheduled counts, update strategy, node selector", async () => {
    await openDetail("apps/daemonsets", "agent");
    const ov = within(await screen.findByTestId("k8s-overview"));
    expect(await ov.findByText("3/4")).toBeInTheDocument();
    expect(ov.getByText("OnDelete")).toBeInTheDocument();
    expect(ov.getByText("disk=ssd")).toBeInTheDocument();
  });

  it("ReplicaSet: replicas, selector and the HPA scaling it", async () => {
    await openDetail("apps/replicasets", "web-abc");
    const ov = within(await screen.findByTestId("k8s-overview"));
    expect(await ov.findByText("3/3")).toBeInTheDocument();
    expect(ov.getByText("app=web,tier in (a,b)")).toBeInTheDocument();
    expect(await ov.findByText("rs-hpa · 2–8")).toBeInTheDocument();
  });

  it("reads HPAs at the version the cluster serves", async () => {
    const id = nextId++;
    mockCluster();
    vi.mocked(GetK8sResourceTypes).mockResolvedValue(
      JSON.stringify({
        ...types,
        types: types.types.map((t) => (t.resource === "horizontalpodautoscalers" ? { ...t, version: "v1" } : t)),
      }) as never
    );
    render(<K8sClusterPage asset={asset(id)} />);
    await screen.findByTestId("k8s-sidebar");
    useK8sStore.getState().openTab(id, { id: "res:apps/deployments:default:web", label: "web" });
    expect(await screen.findByText("web-hpa · 2–8")).toBeInTheDocument();
    const hpaCalls = vi.mocked(ListK8sResources).mock.calls.filter(([r]) => r.resource === "horizontalpodautoscalers");
    expect(hpaCalls.map(([r]) => r.version)).toEqual(["v1"]);
  });

  it.each([
    ["/pods", "web-1", "CrashLoopBackOff"],
    ["batch/jobs", "migrate", "Running"],
    ["apps/daemonsets", "agent", "3/4"],
    ["batch/cronjobs", "nightly", "Suspended"],
  ])("%s header shows the status kubectl would (%s → %s)", async (typeKey, name, status) => {
    await openDetail(typeKey, name);
    const header = await screen.findByTestId("k8s-detail-header");
    expect(await within(header).findByText(status)).toBeInTheDocument();
  });

  it("a failed Job shows Failed in its header", async () => {
    objects.broken = {
      ...job,
      metadata: meta("broken"),
      status: { failed: 6, conditions: [{ type: "Failed", status: "True", reason: "BackoffLimitExceeded" }] },
    };
    await openDetail("batch/jobs", "broken");
    const header = await screen.findByTestId("k8s-detail-header");
    expect(await within(header).findByText("Failed")).toBeInTheDocument();
  });

  it("Job: completions, parallelism, backoff limit and pod counts", async () => {
    await openDetail("batch/jobs", "migrate");
    const ov = within(await screen.findByTestId("k8s-overview"));
    expect(await ov.findByText("4/5")).toBeInTheDocument();
    expect(ov.getByText("2")).toBeInTheDocument(); // parallelism
    expect(ov.getByText("6")).toBeInTheDocument(); // backoff limit
  });

  it("CronJob: schedule, suspended flag, policy and the Jobs it owns", async () => {
    const id = nextId++;
    mockCluster({ jobs: [ownedJob("nightly-1", "nightly"), ownedJob("other-1", "someone-else")] });
    render(<K8sClusterPage asset={asset(id)} />);
    await screen.findByTestId("k8s-sidebar");
    useK8sStore.getState().openTab(id, { id: "res:batch/cronjobs:default:nightly", label: "nightly" });
    const ov = within(await screen.findByTestId("k8s-overview"));

    expect(await ov.findByText("*/5 * * * *")).toBeInTheDocument();
    expect(ov.getByText("Forbid")).toBeInTheDocument();
    expect(ov.getByText("asset.k8sSuspended")).toBeInTheDocument();
    expect(await ov.findByRole("link", { name: "nightly-1" })).toBeInTheDocument();
    expect(ov.queryByText("other-1")).not.toBeInTheDocument();
  });

  it("Pod: keeps IPs, node, ready, QoS, container states / restarts, conditions and a terminal per container", async () => {
    const user = userEvent.setup();
    const id = await openDetail("/pods", "web-1");
    const ov = within(await screen.findByTestId("k8s-overview"));
    expect(await ov.findByText("10.42.0.7")).toBeInTheDocument();
    expect(ov.getByText("192.0.2.5")).toBeInTheDocument();
    expect(ov.getByText("node-1")).toBeInTheDocument();
    expect(ov.getByText("1/2")).toBeInTheDocument();
    expect(ov.getByText("Burstable")).toBeInTheDocument();
    expect(ov.getByText("Waiting: CrashLoopBackOff")).toBeInTheDocument();
    expect(ov.getByText("3")).toBeInTheDocument();
    expect(ov.getAllByText("8080/http").length).toBe(2);
    expect(ov.getAllByText("100m / 128Mi").length).toBe(2);
    expect(ov.getAllByText("1 / 512Mi").length).toBe(2);
    await user.click(ov.getByRole("button", { name: /asset\.k8sPodConditions/ }));
    expect(ov.getByText("AllReady")).toBeInTheDocument();

    const terminals = ov.getAllByRole("button", { name: /asset\.k8sTerminal/ });
    expect(terminals).toHaveLength(2);
    await user.click(terminals[1]);
    const cluster = useK8sStore.getState().clusters[id];
    expect(cluster.tabs.find((t) => t.id === cluster.activeTabId)?.label).toBe("web-1 · sidecar");
    expect(startPodTerminal).toHaveBeenCalledWith(id, cluster.activeTabId);
  });
});

describe("Pods sub-tab", () => {
  it("lists the pods the selector matches, each with a link, logs and (when registered) terminal entry", async () => {
    const user = userEvent.setup();
    const id = await openDetail("apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sPodsTab/ }));

    await screen.findByRole("link", { name: "web-1" });
    expect(vi.mocked(ListK8sResources)).toHaveBeenCalledWith(
      expect.objectContaining({
        group: "",
        resource: "pods",
        namespace: "default",
        labelSelector: "app=web,tier in (a,b)",
        includeObject: true,
      })
    );
    expect(screen.getByRole("link", { name: "web-2" })).toBeInTheDocument();

    const web1 = screen.getByRole("link", { name: "web-1" }).closest("tr")!;
    await user.click(within(web1).getByRole("button", { name: /asset\.k8sLogs/ }));
    expect(useK8sStore.getState().clusters[id].activeTabId).toBe("res:/pods:default:web-1");
    expect(useK8sStore.getState().detailSubTabs[id]["res:/pods:default:web-1"]).toBe("logs");
  });

  it("opens a terminal on a row's Pod in its first container", async () => {
    const user = userEvent.setup();
    const id = await openDetail("apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sPodsTab/ }));
    const web2 = (await screen.findByRole("link", { name: "web-2" })).closest("tr")!;
    await user.click(within(web2).getByRole("button", { name: /asset\.k8sTerminal/ }));

    const cluster = useK8sStore.getState().clusters[id];
    expect(cluster.tabs.find((t) => t.id === cluster.activeTabId)?.label).toBe("web-2 · app");
    expect(useK8sStore.getState().terminals[id][cluster.activeTabId]).toMatchObject({
      namespace: "default",
      pod: "web-2",
      container: "app",
      containers: ["app", "sidecar"],
      shell: "auto",
    });
  });

  it("greys a row's terminal entry with the reason when its Pod is not running", async () => {
    const user = userEvent.setup();
    const running = objects["web-2"];
    objects["web-2"] = pod("web-2", { status: { phase: "Pending" } });
    try {
      const id = await openDetail("apps/deployments", "web");
      await user.click(await screen.findByRole("tab", { name: /asset\.k8sPodsTab/ }));
      const web2 = (await screen.findByRole("link", { name: "web-2" })).closest("tr")!;
      const button = within(web2).getByRole("button", { name: /asset\.k8sTerminal/ });
      expect(button).toBeDisabled();
      expect(button.parentElement?.getAttribute("title")).toMatch(/Pending/);
      const web1 = screen.getByRole("link", { name: "web-1" }).closest("tr")!;
      expect(within(web1).getByRole("button", { name: /asset\.k8sTerminal/ })).toBeEnabled();
      expect(useK8sStore.getState().terminals[id]).toBeUndefined();
    } finally {
      objects["web-2"] = running;
    }
  });

  it("reads the matched pods once across inner-tab switches", async () => {
    const user = userEvent.setup();
    const id = await openDetail("apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sPodsTab/ }));
    await screen.findByRole("link", { name: "web-1" });

    useK8sStore.getState().setActiveTab(id, "overview");
    await screen.findByTestId("k8s-stat-version");
    useK8sStore.getState().setActiveTab(id, "res:apps/deployments:default:web");
    await screen.findByRole("link", { name: "web-1" });

    const podReads = vi.mocked(ListK8sResources).mock.calls.filter(([r]) => r.resource === "pods" && r.includeObject);
    expect(podReads).toHaveLength(1);
  });

  it("opens the Pod detail from the pod name", async () => {
    const user = userEvent.setup();
    const id = await openDetail("apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sPodsTab/ }));
    await user.click(await screen.findByRole("link", { name: "web-2" }));
    expect(useK8sStore.getState().clusters[id].activeTabId).toBe("res:/pods:default:web-2");
  });

  it("does not ask the cluster for pods when the workload has no selector", async () => {
    const user = userEvent.setup();
    objects.bare = { ...deployment, metadata: meta("bare"), spec: { ...deployment.spec, selector: undefined } };
    await openDetail("apps/deployments", "bare");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sPodsTab/ }));
    expect(await screen.findByText("asset.k8sNoPods")).toBeInTheDocument();
    expect(vi.mocked(ListK8sResources).mock.calls.some(([r]) => r.resource === "pods")).toBe(false);
  });
});

describe("Logs sub-tab", () => {
  it("workload: switching pod and container starts the stream for the chosen pod / container", async () => {
    const user = userEvent.setup();
    await openDetail("apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sLogs/ }));

    await user.selectOptions(await screen.findByDisplayValue("web-1"), "web-2");
    await user.selectOptions(screen.getByDisplayValue("app"), "sidecar");
    await user.click(screen.getByRole("button", { name: /asset\.k8sPodLogsStart/ }));

    await waitFor(() =>
      expect(StartK8sPodLogs).toHaveBeenCalledWith(expect.any(Number), "default", "web-2", "sidecar", 200)
    );
  });

  it("workload: a failed refresh of its pods keeps the log panel (and its stream) with the reason", async () => {
    const user = userEvent.setup();
    const id = await openDetail("apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sLogs/ }));
    await screen.findByTestId("k8s-log-terminal");

    const ok = vi.mocked(ListK8sResources).getMockImplementation()!;
    vi.mocked(ListK8sResources).mockImplementation((async (req: k8s.K8sListRequest) => {
      if (req.resource === "pods") throw new Error("pods: i/o timeout");
      return ok(req);
    }) as never);
    await useK8sStore.getState().refreshAll(id);

    expect((await screen.findByTestId("k8s-stale-error")).textContent).toContain("pods: i/o timeout");
    expect(screen.getByTestId("k8s-log-terminal")).toBeInTheDocument();
  });

  it("pod: streams its own logs with a container switch but no pod switch", async () => {
    const user = userEvent.setup();
    await openDetail("/pods", "web-1");
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sLogs/ }));

    await user.selectOptions(await screen.findByDisplayValue("app"), "sidecar");
    expect(screen.queryByDisplayValue("web-2")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /asset\.k8sPodLogsStart/ }));
    await waitFor(() =>
      expect(StartK8sPodLogs).toHaveBeenCalledWith(expect.any(Number), "default", "web-1", "sidecar", 200)
    );
  });
});

describe("logs action", () => {
  it.each([
    ["/pods", true],
    ["apps/deployments", true],
    ["apps/statefulsets", true],
    ["apps/daemonsets", true],
    ["apps/replicasets", true],
    ["batch/jobs", true],
    ["batch/cronjobs", false],
    ["/configmaps", false],
  ])("is offered for %s: %s", async (typeKey, offered) => {
    await openDetail("/pods", "web-1"); // registration happens with the page
    const target = row(deployment) as unknown as K8sResourceRow;
    expect(getK8sActions(typeKey, target).some((a) => a.id === "logs")).toBe(offered);
  });

  it("opens the resource's detail on its Logs sub-tab", async () => {
    const id = await openDetail("/pods", "web-1");
    const logs = getK8sActions("apps/deployments", row(deployment) as unknown as K8sResourceRow).find(
      (a) => a.id === "logs"
    )!;
    await logs.run({ assetId: id, typeKey: "apps/deployments", namespace: "default", name: "web" });

    expect(useK8sStore.getState().clusters[id].activeTabId).toBe("res:apps/deployments:default:web");
    expect(await screen.findByRole("tab", { name: /asset\.k8sLogs/, selected: true })).toBeInTheDocument();
    expect(within(screen.getByTestId("k8s-detail-header")).getByText("web")).toBeInTheDocument();
  });
});
