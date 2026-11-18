import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { K8sYamlTab } from "@/components/k8s/detail/K8sYamlTab";
import { K8sResourceGrid } from "@/components/k8s/cluster/K8sResourceGrid";

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

describe("K8s copy failures", () => {
  beforeEach(() => {
    vi.mocked(toast.error).mockReset();
    const writeText = vi.fn().mockRejectedValue(new Error("clipboard denied"));
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  });

  it("reports a failed YAML copy instead of claiming it was copied", async () => {
    render(<K8sYamlTab yaml="kind: Pod" onEdit={undefined} />);
    fireEvent.click(screen.getByRole("button", { name: /action\.copy/ }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("clipboard denied"));
  });

  it("reports a failed name copy from a row's menu", async () => {
    render(
      <K8sResourceGrid
        assetId={1}
        typeKey="/configmaps"
        columns={[{ name: "Name", type: "string", format: "name" }]}
        rows={[
          {
            name: "cfg",
            namespace: "default",
            uid: "u",
            creationTimestamp: "",
            labels: {},
            ownerReferences: [],
            cells: ["cfg"],
          },
        ]}
        sort={{ column: "Name", dir: "asc" }}
        onSort={() => {}}
        onOpen={() => {}}
      />
    );
    fireEvent.contextMenu(screen.getByTestId("k8s-row"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "asset.k8sCopyName" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("clipboard denied"));
  });
});
