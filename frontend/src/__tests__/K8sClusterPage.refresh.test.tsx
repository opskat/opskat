import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import {
  GetK8sClusterInfo,
  GetK8sNamespacePods,
  GetK8sNamespaceResources,
  GetK8sPodDetail,
} from "../../wailsjs/go/k8s/K8s";
import type { asset_entity } from "../../wailsjs/go/models";

const clusterInfo = {
  version: "1.34.1",
  platform: "linux/amd64",
  nodes: [],
  namespaces: [{ name: "default", status: "Active" }],
};

const namespaceResources = {
  namespace: "default",
  pods: 1,
  deployments: 0,
  services: 0,
  config_maps: 0,
  secrets: 0,
  pvcs: 0,
  service_accounts: 0,
};

function pod(name: string, status: string) {
  return {
    name,
    namespace: "default",
    status,
    node_name: "node-1",
    pod_ip: "10.0.0.1",
    age: "1m",
    ready: status === "Running" ? "1/1" : "0/1",
    restart_count: 0,
  };
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
    containers: [
      {
        name: "app",
        image,
        state: "Running",
        ready: true,
        restart_count: 0,
      },
    ],
    conditions: [],
    events: [],
    labels: {},
    annotations: {},
    yaml: `image: ${image}`,
  };
}

function assetWithID(ID: number) {
  return {
    ID,
    Name: "k8s",
    Type: "k8s",
    Config: '{"namespace":"default"}',
  } as asset_entity.Asset;
}

describe("K8sClusterPage refresh", () => {
  beforeEach(() => {
    vi.mocked(GetK8sClusterInfo).mockReset();
    vi.mocked(GetK8sNamespaceResources).mockReset();
    vi.mocked(GetK8sNamespacePods).mockReset();
    vi.mocked(GetK8sPodDetail).mockReset();
  });

  it("refreshes already-loaded pod lists from the cluster", async () => {
    const user = userEvent.setup();
    vi.mocked(GetK8sClusterInfo).mockResolvedValue(JSON.stringify(clusterInfo) as never);
    vi.mocked(GetK8sNamespaceResources).mockResolvedValue(JSON.stringify(namespaceResources) as never);
    vi.mocked(GetK8sNamespacePods)
      .mockResolvedValueOnce(JSON.stringify([pod("api-old", "Running")]) as never)
      .mockResolvedValueOnce(JSON.stringify([pod("api-new", "Pending")]) as never);

    render(<K8sClusterPage asset={assetWithID(99001)} />);

    const podLabels = await screen.findAllByText("asset.k8sPods");
    await user.click(podLabels[0]!);
    await screen.findByText("api-old");

    await user.click(screen.getAllByRole("button", { name: "action.refresh" })[0]!);

    await waitFor(() => {
      expect(GetK8sNamespacePods).toHaveBeenCalledTimes(2);
    });
    expect(await screen.findByText("api-new")).toBeInTheDocument();
    expect(screen.queryByText("api-old")).not.toBeInTheDocument();
  });

  it("refreshes details for open pod tabs", async () => {
    const user = userEvent.setup();
    vi.mocked(GetK8sClusterInfo).mockResolvedValue(JSON.stringify(clusterInfo) as never);
    vi.mocked(GetK8sNamespaceResources).mockResolvedValue(JSON.stringify(namespaceResources) as never);
    vi.mocked(GetK8sNamespacePods).mockResolvedValue(JSON.stringify([pod("api", "Running")]) as never);
    vi.mocked(GetK8sPodDetail)
      .mockResolvedValueOnce(JSON.stringify(podDetail("api", "example/api:old")) as never)
      .mockResolvedValueOnce(JSON.stringify(podDetail("api", "example/api:new")) as never);

    render(<K8sClusterPage asset={assetWithID(99002)} />);

    const podLabels = await screen.findAllByText("asset.k8sPods");
    await user.click(podLabels[0]!);
    await user.click(await screen.findByText("api"));
    await screen.findByText("example/api:old");

    await user.click(screen.getAllByRole("button", { name: "action.refresh" })[0]!);

    await waitFor(() => {
      expect(GetK8sPodDetail).toHaveBeenCalledTimes(2);
    });
    expect(await screen.findByText("example/api:new")).toBeInTheDocument();
    expect(screen.queryByText("example/api:old")).not.toBeInTheDocument();
  });
});
