import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Trash2 } from "lucide-react";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { registerK8sAction } from "@/components/k8s/actions";
import {
  registerDetailOverview,
  registerDetailSubTab,
  registerYamlEditHandler,
} from "@/components/k8s/detail/registry";
import { useK8sStore } from "@/stores/k8sStore";
import {
  GetK8sClusterVersion,
  GetK8sResource,
  GetK8sResourceTypes,
  ListK8sEvents,
  ListK8sResources,
} from "../../wailsjs/go/k8s/K8s";
import type { asset_entity } from "../../wailsjs/go/models";

const copied = vi.hoisted(() => vi.fn());
vi.mock("@/lib/notify", () => ({ notifyCopied: copied, notifySuccess: vi.fn() }));

const type = (group: string, version: string, resource: string, kind: string, category: string) => ({
  group,
  version,
  resource,
  kind,
  namespaced: true,
  verbs: ["list"],
  category,
});
const types = {
  types: [
    type("apps", "v1", "deployments", "Deployment", "workloads"),
    type("apps", "v1", "replicasets", "ReplicaSet", "workloads"),
    type("", "v1", "configmaps", "ConfigMap", "config"),
  ],
  failedGroups: [],
};

const deployment = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  metadata: {
    name: "web",
    namespace: "default",
    uid: "uid-web",
    creationTimestamp: "2026-11-10T08:00:00Z",
    labels: { app: "web" },
    annotations: { note: "hello-annotation" },
  },
  spec: { replicas: 3 },
  status: {
    replicas: 3,
    readyReplicas: 2,
    conditions: [{ type: "Available", status: "True", reason: "MinimumReplicasAvailable" }],
  },
};
const replicaSet = {
  apiVersion: "apps/v1",
  kind: "ReplicaSet",
  metadata: {
    name: "web-abc",
    namespace: "default",
    uid: "uid-rs",
    creationTimestamp: "2026-11-10T08:00:00Z",
    ownerReferences: [
      { apiVersion: "v1", kind: "ConfigMap", name: "other", controller: false },
      { apiVersion: "apps/v1", kind: "Deployment", name: "web", controller: true },
    ],
  },
};
const YAML = "kind: Deployment\nmetadata:\n  name: web\n";

let nextId = 92000;
const asset = (ID: number) => ({ ID, Name: "k8s", Type: "k8s", Config: "{}" }) as asset_entity.Asset;

function mockCluster(objects: Record<string, unknown>) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(JSON.stringify({ version: "v1", platform: "x" }) as never);
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(types) as never);
  vi.mocked(ListK8sResources).mockResolvedValue(JSON.stringify({ columns: [], rows: [] }) as never);
  vi.mocked(GetK8sResource).mockImplementation((async (ref: { name: string }) =>
    JSON.stringify({ notFound: false, object: objects[ref.name], yaml: YAML })) as never);
  vi.mocked(ListK8sEvents).mockResolvedValue(
    JSON.stringify({
      events: [
        { type: "Warning", reason: "NewestWarn", message: "m1", count: 1, lastTimestamp: "2026-11-16T10:00:00Z" },
        { type: "Normal", reason: "MiddleNormal", message: "m2", count: 1, lastTimestamp: "2026-11-16T09:00:00Z" },
        { type: "Warning", reason: "OldestWarn", message: "m3", count: 2, lastTimestamp: "2026-11-16T08:00:00Z" },
      ],
    }) as never
  );
}

async function openDetail(id: number, typeKey: string, name: string) {
  render(<K8sClusterPage asset={asset(id)} />);
  await screen.findByTestId("k8s-sidebar");
  useK8sStore.getState().openTab(id, { id: `res:${typeKey}:default:${name}`, label: name });
}

describe("K8s generic resource detail", () => {
  beforeEach(() => {
    vi.mocked(GetK8sResource).mockReset();
    vi.mocked(ListK8sEvents).mockReset();
    copied.mockReset();
  });

  it("shows the header with derived status, kind and namespace, created time and an overview", async () => {
    mockCluster({ web: deployment });
    const id = nextId++;
    await openDetail(id, "apps/deployments", "web");

    const header = await screen.findByTestId("k8s-detail-header");
    expect(within(header).getByText("web")).toBeInTheDocument();
    expect(within(header).getByText("2/3")).toBeInTheDocument();
    expect(within(header).getByText(/Deployment/)).toBeInTheDocument();
    expect(within(header).getByText(/default/)).toBeInTheDocument();
    expect(within(header).getByText(/2026/)).toBeInTheDocument();
    // generic overview: conditions shown, labels / annotations collapsed
    expect(await screen.findByText("MinimumReplicasAvailable")).toBeInTheDocument();
    expect(screen.queryByText("hello-annotation", { exact: false })).not.toBeInTheDocument();
    expect(screen.queryByText("app: web")).not.toBeInTheDocument();
  });

  it("opens the controller owner's detail from the header link", async () => {
    const user = userEvent.setup();
    mockCluster({ "web-abc": replicaSet, web: deployment });
    const id = nextId++;
    await openDetail(id, "apps/replicasets", "web-abc");

    await user.click(await screen.findByRole("link", { name: /web/, hidden: false }));
    expect(useK8sStore.getState().clusters[id].activeTabId).toBe("res:apps/deployments:default:web");
    await waitFor(() =>
      expect(vi.mocked(GetK8sResource)).toHaveBeenCalledWith(expect.objectContaining({ name: "web" }))
    );
  });

  it("lists events newest first with the warning count on the sub-tab", async () => {
    const user = userEvent.setup();
    mockCluster({ web: deployment });
    await openDetail(nextId++, "apps/deployments", "web");

    const tab = await screen.findByRole("tab", { name: /asset\.k8sEvents/ });
    await waitFor(() => expect(within(tab).getByText("2")).toBeInTheDocument());
    await user.click(tab);
    const reasons = (await screen.findAllByTestId("k8s-event-reason")).map((e) => e.textContent);
    expect(reasons).toEqual(["NewestWarn", "MiddleNormal", "OldestWarn"]);
    expect(vi.mocked(ListK8sEvents)).toHaveBeenCalledWith(
      expect.objectContaining({ namespace: "default", involvedUid: "uid-web" })
    );
  });

  it("shows the YAML read-only, copyable, and offers edit only when a handler is registered", async () => {
    const user = userEvent.setup();
    mockCluster({ web: deployment });
    const id = nextId++;
    await openDetail(id, "apps/deployments", "web");

    await user.click(await screen.findByRole("tab", { name: /YAML/ }));
    expect(await screen.findByTestId("k8s-yaml")).toHaveTextContent("kind: Deployment");
    expect(screen.getByTestId("k8s-yaml").textContent).not.toContain("managedFields");
    expect(screen.queryByRole("button", { name: /asset\.k8sEditYaml/ })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /action\.copy/ }));
    expect(await navigator.clipboard.readText()).toBe(YAML);
    expect(copied).toHaveBeenCalled();

    const onEdit = vi.fn();
    const off = registerYamlEditHandler(onEdit);
    try {
      // handler registered after mount: switch tab away and back to re-render
      await user.click(screen.getByRole("tab", { name: /asset\.k8sOverview/ }));
      await user.click(screen.getByRole("tab", { name: /YAML/ }));
      await user.click(await screen.findByRole("button", { name: /asset\.k8sEditYaml/ }));
      expect(onEdit).toHaveBeenCalledWith(
        expect.objectContaining({ assetId: id, typeKey: "apps/deployments", namespace: "default", name: "web" })
      );
    } finally {
      off();
    }
  });

  it("puts registered actions in the header with destructive ones last in the error colour", async () => {
    mockCluster({ web: deployment });
    const run = vi.fn();
    const offs = [
      registerK8sAction({
        id: "t-delete",
        labelKey: "t.delete",
        icon: Trash2,
        destructive: true,
        appliesTo: (k) => k === "apps/deployments",
        run,
      }),
      registerK8sAction({
        id: "t-restart",
        labelKey: "t.restart",
        icon: Trash2,
        appliesTo: (k) => k === "apps/deployments",
        run: vi.fn(),
      }),
    ];
    try {
      const user = userEvent.setup();
      const id = nextId++;
      await openDetail(id, "apps/deployments", "web");
      const header = await screen.findByTestId("k8s-detail-header");
      // the built-in actions (logs…) come first; only the ones registered here are compared
      const buttons = within(header)
        .getAllByRole("button")
        .filter((b) => b.textContent?.startsWith("t."));
      expect(buttons.map((b) => b.textContent)).toEqual(["t.restart", "t.delete"]);
      expect(buttons[1].className).toContain("text-destructive");
      await user.click(buttons[1]);
      expect(run).toHaveBeenCalledWith(
        expect.objectContaining({ assetId: id, typeKey: "apps/deployments", namespace: "default", name: "web" })
      );
    } finally {
      offs.forEach((off) => off());
    }
  });

  it("does not re-read the resource when switching inner tabs, but the sidebar refresh does", async () => {
    const user = userEvent.setup();
    mockCluster({ web: deployment });
    const id = nextId++;
    await openDetail(id, "apps/deployments", "web");
    await user.click(await screen.findByRole("tab", { name: /YAML/ }));
    await user.click(screen.getByRole("tab", { name: /asset\.k8sOverview/ }));
    await user.click(screen.getByRole("tab", { name: /asset\.k8sEvents/ }));
    expect(vi.mocked(GetK8sResource)).toHaveBeenCalledTimes(1);

    await useK8sStore.getState().refreshAll(id);
    expect(vi.mocked(GetK8sResource)).toHaveBeenCalledTimes(2);
  });

  it("says the resource no longer exists after a refresh finds it deleted", async () => {
    mockCluster({ web: deployment });
    const id = nextId++;
    await openDetail(id, "apps/deployments", "web");
    await screen.findByTestId("k8s-detail-header");

    vi.mocked(GetK8sResource).mockResolvedValue(JSON.stringify({ notFound: true, object: null, yaml: "" }) as never);
    await useK8sStore.getState().refreshAll(id);
    expect(await screen.findByText("asset.k8sResourceGone")).toBeInTheDocument();
    expect(screen.queryByTestId("k8s-detail-header")).not.toBeInTheDocument();
  });

  it("fails only the events region when events can't be read, showing the reason verbatim", async () => {
    const user = userEvent.setup();
    mockCluster({ web: deployment });
    vi.mocked(ListK8sEvents).mockRejectedValue(new Error('events is forbidden: User "x" cannot list'));
    await openDetail(nextId++, "apps/deployments", "web");

    expect(await screen.findByTestId("k8s-detail-header")).toBeInTheDocument();
    await user.click(await screen.findByRole("tab", { name: /asset\.k8sEvents/ }));
    expect(await screen.findByText('events is forbidden: User "x" cannot list')).toBeInTheDocument();
    expect(screen.getByTestId("k8s-detail-header")).toBeInTheDocument();
  });

  it("shows the cluster's reason with retry when the resource itself can't be read", async () => {
    mockCluster({ web: deployment });
    vi.mocked(GetK8sResource).mockRejectedValue(new Error("deployments is forbidden"));
    await openDetail(nextId++, "apps/deployments", "web");
    expect(await screen.findByText("deployments is forbidden")).toBeInTheDocument();
    expect(screen.getByTestId("async-state-retry")).toBeInTheDocument();
  });

  it("lets a kind plug in its own overview and extra sub-tabs", async () => {
    mockCluster({ web: deployment });
    const offs = [
      registerDetailOverview("Deployment", ({ name }) => <div>custom overview of {name}</div>),
      registerDetailSubTab("Deployment", { id: "pods", labelKey: "t.podsTab", Component: () => <div>pods body</div> }),
    ];
    try {
      const user = userEvent.setup();
      await openDetail(nextId++, "apps/deployments", "web");
      expect(await screen.findByText("custom overview of web")).toBeInTheDocument();
      await user.click(screen.getByRole("tab", { name: "t.podsTab" }));
      expect(await screen.findByText("pods body")).toBeInTheDocument();
    } finally {
      offs.forEach((off) => off());
    }
  });
});
