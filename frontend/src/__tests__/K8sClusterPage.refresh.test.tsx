import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { GetK8sClusterVersion, GetK8sPodDetail, GetK8sResourceTypes, ListK8sResources } from "../../wailsjs/go/k8s/K8s";
import type { asset_entity, k8s } from "../../wailsjs/go/models";

const types = {
  types: [
    {
      group: "",
      version: "v1",
      resource: "pods",
      kind: "Pod",
      namespaced: true,
      verbs: ["list"],
      category: "workloads",
    },
  ],
  failedGroups: [],
};

function podTable(names: string[]) {
  return JSON.stringify({
    columns: [
      { name: "Name", type: "string", format: "name" },
      { name: "Status", type: "string", format: "" },
    ],
    rows: names.map((name) => ({
      name,
      namespace: "default",
      uid: name,
      creationTimestamp: "2026-11-16T00:00:00Z",
      labels: {},
      ownerReferences: [],
      cells: [name, "Running"],
    })),
  });
}

function podDetail(name: string, image: string) {
  return {
    name,
    namespace: "default",
    status: "Running",
    node_name: "node-1",
    pod_ip: "10.0.0.1",
    host_ip: "192.168.0.1",
    creation_time: "2026-06-30T00:00:00Z",
    age: "1m",
    ready: "1/1",
    restart_count: 0,
    qos_class: "BestEffort",
    containers: [{ name: "app", image, state: "Running", ready: true, restart_count: 0 }],
    conditions: [],
    events: [],
    labels: {},
    annotations: {},
    yaml: `image: ${image}`,
  };
}

function asset(ID: number) {
  return { ID, Name: "k8s", Type: "k8s", Config: '{"namespace":"default"}' } as asset_entity.Asset;
}

function mockCluster(pods: () => string) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(
    JSON.stringify({ version: "v1.31.4", platform: "linux/amd64" }) as never
  );
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(types) as never);
  vi.mocked(ListK8sResources).mockImplementation((async (req: k8s.K8sListRequest) => {
    if (req.resource === "namespaces") return podTable(["default"]);
    return pods();
  }) as never);
}

function podListCalls() {
  return vi.mocked(ListK8sResources).mock.calls.filter(([req]) => req.resource === "pods").length;
}

async function openPods(user: ReturnType<typeof userEvent.setup>) {
  const sidebar = await screen.findByTestId("k8s-sidebar");
  await user.click(await within(sidebar).findByText("Pod"));
}

describe("K8sClusterPage refresh (#212)", () => {
  beforeEach(() => {
    vi.mocked(GetK8sClusterVersion).mockReset();
    vi.mocked(GetK8sResourceTypes).mockReset();
    vi.mocked(ListK8sResources).mockReset();
    vi.mocked(GetK8sPodDetail).mockReset();
  });

  it("re-reads an already loaded list and shows the new rows", async () => {
    const user = userEvent.setup();
    let names = ["api-old"];
    mockCluster(() => podTable(names));

    render(<K8sClusterPage asset={asset(99001)} />);
    await openPods(user);
    await screen.findByText("api-old");

    names = ["api-new"];
    await user.click(within(screen.getByTestId("k8s-sidebar")).getByRole("button", { name: "query.refreshTree" }));

    expect(await screen.findByText("api-new")).toBeInTheDocument();
    expect(screen.queryByText("api-old")).not.toBeInTheDocument();
    expect(podListCalls()).toBe(2);
  });

  it("re-reads an open pod detail", async () => {
    const user = userEvent.setup();
    mockCluster(() => podTable(["api"]));
    vi.mocked(GetK8sPodDetail)
      .mockResolvedValueOnce(JSON.stringify(podDetail("api", "example/api:old")) as never)
      .mockResolvedValueOnce(JSON.stringify(podDetail("api", "example/api:new")) as never);

    render(<K8sClusterPage asset={asset(99002)} />);
    await openPods(user);
    await user.click(await screen.findByText("api"));
    await screen.findByText("example/api:old");

    await user.click(within(screen.getByTestId("k8s-sidebar")).getByRole("button", { name: "query.refreshTree" }));

    await waitFor(() => expect(GetK8sPodDetail).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("example/api:new")).toBeInTheDocument();
    expect(screen.queryByText("example/api:old")).not.toBeInTheDocument();
  });
});
