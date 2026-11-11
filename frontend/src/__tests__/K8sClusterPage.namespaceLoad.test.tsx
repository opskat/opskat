import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { GetK8sClusterInfo, GetK8sNamespaceResources } from "../../wailsjs/go/k8s/K8s";
import type { asset_entity } from "../../wailsjs/go/models";

const clusterInfo = {
  version: "1.34.1",
  platform: "linux/amd64",
  nodes: [],
  namespaces: [{ name: "default", status: "Active" }],
};

const namespaceResources = {
  namespace: "default",
  pods: 3,
  deployments: 0,
  services: 0,
  config_maps: 0,
  secrets: 0,
  pvcs: 0,
  service_accounts: 0,
};

describe("K8sClusterPage sidebar namespace load state", () => {
  beforeEach(() => {
    vi.mocked(GetK8sClusterInfo).mockReset();
    vi.mocked(GetK8sNamespaceResources).mockReset();
  });

  it("shows the namespace failure reason with a retry in the sidebar, and retry reloads it", async () => {
    const user = userEvent.setup();
    vi.mocked(GetK8sClusterInfo).mockResolvedValue(JSON.stringify(clusterInfo) as never);
    vi.mocked(GetK8sNamespaceResources).mockRejectedValueOnce(new Error("namespaces is forbidden"));

    render(
      <K8sClusterPage
        asset={{ ID: 99101, Name: "k8s", Type: "k8s", Config: '{"namespace":"default"}' } as asset_entity.Asset}
      />
    );

    const sidebar = await screen.findByTestId("k8s-sidebar");
    const alert = await within(sidebar).findByRole("alert");
    expect(alert).toHaveTextContent("namespaces is forbidden");

    vi.mocked(GetK8sNamespaceResources).mockResolvedValue(JSON.stringify(namespaceResources) as never);
    await user.click(within(alert).getByTestId("async-state-retry"));
    expect(await within(sidebar).findByText("asset.k8sPods")).toBeInTheDocument();
    expect(within(sidebar).queryByRole("alert")).not.toBeInTheDocument();
  });
});
