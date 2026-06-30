import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import {
  GetK8sClusterInfo,
  GetK8sNamespacePods,
  GetK8sNamespaceResources,
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

const asset = {
  ID: 99001,
  Name: "k8s",
  Type: "k8s",
  Config: '{"namespace":"default"}',
} as asset_entity.Asset;

describe("K8sClusterPage refresh", () => {
  it("refreshes already-loaded pod lists from the cluster", async () => {
    const user = userEvent.setup();
    vi.mocked(GetK8sClusterInfo).mockResolvedValue(JSON.stringify(clusterInfo) as never);
    vi.mocked(GetK8sNamespaceResources).mockResolvedValue(JSON.stringify(namespaceResources) as never);
    vi.mocked(GetK8sNamespacePods)
      .mockResolvedValueOnce(JSON.stringify([pod("api-old", "Running")]) as never)
      .mockResolvedValueOnce(JSON.stringify([pod("api-new", "Pending")]) as never);

    render(<K8sClusterPage asset={asset} />);

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
});
