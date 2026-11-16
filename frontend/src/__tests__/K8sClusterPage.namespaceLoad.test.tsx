import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { GetK8sClusterVersion, GetK8sResourceTypes, ListK8sResources } from "../../wailsjs/go/k8s/K8s";
import type { asset_entity, k8s } from "../../wailsjs/go/models";

const types = {
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
      resource: "events",
      kind: "Event",
      namespaced: true,
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
      group: "events.k8s.io",
      version: "v1",
      resource: "events",
      kind: "Event",
      namespaced: true,
      verbs: ["list"],
      category: "custom",
    },
    {
      group: "cert-manager.io",
      version: "v1",
      resource: "certificates",
      kind: "Certificate",
      namespaced: true,
      verbs: ["list"],
      category: "custom",
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
      uid: name,
      creationTimestamp: "2026-11-16T00:00:00Z",
      labels: {},
      ownerReferences: [],
      cells: [name, "Running"],
    })),
  });
}

type ListHandler = (req: k8s.K8sListRequest) => Promise<string>;

const listOK: ListHandler = async (req) => {
  if (req.resource === "namespaces") return table(["default", "prod"]);
  if (req.resource === "nodes") return table(["node-1"]);
  return table([`${req.resource}-in-${req.namespace || "all"}`], req.namespace);
};

function mockCluster(list: ListHandler = listOK) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(
    JSON.stringify({ version: "v1.31.4", platform: "linux/amd64" }) as never
  );
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(types) as never);
  vi.mocked(ListK8sResources).mockImplementation(list as never);
}

function asset(ID: number, namespace = "prod") {
  return { ID, Name: "k8s", Type: "k8s", Config: JSON.stringify({ namespace }) } as asset_entity.Asset;
}

function podListNamespaces() {
  return vi
    .mocked(ListK8sResources)
    .mock.calls.filter(([req]) => req.resource === "pods")
    .map(([req]) => req.namespace);
}

async function openPods(user: ReturnType<typeof userEvent.setup>) {
  const sidebar = await screen.findByTestId("k8s-sidebar");
  await user.click(await within(sidebar).findByText("Pod"));
}

describe("K8sClusterPage loading, namespaces and failures", () => {
  beforeEach(() => {
    vi.mocked(GetK8sClusterVersion).mockReset();
    vi.mocked(GetK8sResourceTypes).mockReset();
    vi.mocked(ListK8sResources).mockReset();
  });

  it("blocks the whole page only when the cluster version can't be read, and retry recovers", async () => {
    const user = userEvent.setup();
    mockCluster();
    vi.mocked(GetK8sClusterVersion).mockRejectedValueOnce(new Error("dial tcp 10.0.0.1:6443: connection refused"));

    render(<K8sClusterPage asset={asset(99101)} />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("dial tcp 10.0.0.1:6443: connection refused");
    expect(screen.queryByTestId("k8s-sidebar")).not.toBeInTheDocument();

    await user.click(within(alert).getByTestId("async-state-retry"));
    expect(await screen.findByTestId("k8s-sidebar")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps a resource-type discovery failure inside the sidebar", async () => {
    mockCluster();
    vi.mocked(GetK8sResourceTypes).mockRejectedValueOnce(
      new Error("the server is currently unable to handle the request")
    );

    render(<K8sClusterPage asset={asset(99102)} />);

    const sidebar = await screen.findByTestId("k8s-sidebar");
    expect(await within(sidebar).findByRole("alert")).toHaveTextContent(
      "the server is currently unable to handle the request"
    );
    expect(await screen.findByText("node-1")).toBeInTheDocument();
  });

  it("lists each kind once, with Events only under the cluster category", async () => {
    mockCluster();
    render(<K8sClusterPage asset={asset(99103)} />);

    const sidebar = await screen.findByTestId("k8s-sidebar");
    expect(await within(sidebar).findByText("Certificate")).toBeInTheDocument();
    expect(within(sidebar).getAllByText("asset.k8sEvents")).toHaveLength(1);
    expect(within(sidebar).queryByText("Event")).not.toBeInTheDocument();
    expect(within(sidebar).queryByText("events.k8s.io")).not.toBeInTheDocument();
  });

  it("reads lists in the asset's namespace by default", async () => {
    const user = userEvent.setup();
    mockCluster();
    render(<K8sClusterPage asset={asset(99104)} />);

    await openPods(user);

    expect(await screen.findByText("pods-in-prod")).toBeInTheDocument();
    expect(podListNamespaces()).toEqual(["prod"]);
  });

  it("reads lists across all namespaces when the asset configures none", async () => {
    const user = userEvent.setup();
    mockCluster();
    render(<K8sClusterPage asset={asset(99105, "")} />);

    await openPods(user);

    expect(await screen.findByText("pods-in-all")).toBeInTheDocument();
    expect(podListNamespaces()).toEqual([""]);
  });

  it("lets the user type a namespace when namespaces can't be listed", async () => {
    const user = userEvent.setup();
    mockCluster(async (req) => {
      if (req.resource === "namespaces") throw new Error('namespaces is forbidden: User "ro" cannot list');
      return listOK(req);
    });
    render(<K8sClusterPage asset={asset(99106)} />);
    await openPods(user);
    await screen.findByText("pods-in-prod");

    const input = await screen.findByRole("textbox", { name: "asset.k8sNamespace" });
    expect(input).toHaveValue("prod");
    await user.clear(input);
    await user.type(input, "team-a{Enter}");

    expect(await screen.findByText("pods-in-team-a")).toBeInTheDocument();
    expect(podListNamespaces()).toEqual(["prod", "team-a"]);
  });

  it("shows a failed list's reason with a retry instead of the empty state", async () => {
    const user = userEvent.setup();
    let fail = true;
    mockCluster(async (req) => {
      if (req.resource === "pods") {
        if (fail) throw new Error('pods is forbidden: User "ro" cannot list resource "pods"');
        return table([]);
      }
      return listOK(req);
    });
    render(<K8sClusterPage asset={asset(99107)} />);
    await openPods(user);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent('pods is forbidden: User "ro" cannot list resource "pods"');
    expect(screen.queryByText("asset.k8sEmptyInNamespace")).not.toBeInTheDocument();

    fail = false;
    await user.click(within(alert).getByTestId("async-state-retry"));
    expect(await screen.findByText("asset.k8sEmptyInNamespace")).toBeInTheDocument();
  });

  it("keeps open tabs and loaded lists across a page unmount without re-reading", async () => {
    const user = userEvent.setup();
    mockCluster();
    const { unmount } = render(<K8sClusterPage asset={asset(99108)} />);
    await openPods(user);
    await screen.findByText("pods-in-prod");

    unmount();
    render(<K8sClusterPage asset={asset(99108)} />);

    expect(await screen.findByText("pods-in-prod")).toBeInTheDocument();
    await waitFor(() => expect(podListNamespaces()).toEqual(["prod"]));
    expect(GetK8sClusterVersion).toHaveBeenCalledTimes(1);
  });
});
