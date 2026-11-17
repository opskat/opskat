import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { getK8sActions } from "@/components/k8s/actions";
import { K8sOpDialogs } from "@/components/k8s/ops/K8sOpDialogs";
import "@/components/k8s/ops/register";
import { useK8sStore, type K8sClusterState, type K8sResourceRow } from "@/stores/k8sStore";
import {
  DeleteK8sResource,
  GetK8sResource,
  ListK8sResources,
  RestartK8sWorkload,
  ScaleK8sResource,
} from "../../wailsjs/go/k8s/K8s";

const h = vi.hoisted(() => ({ notifySuccess: vi.fn(), toastError: vi.fn() }));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: h.notifySuccess }));
vi.mock("sonner", () => ({ toast: { error: h.toastError, success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

const ASSET = 94000;

const type = (group: string, resource: string, kind: string, verbs = ["list", "delete", "patch"]) => ({
  group,
  version: "v1",
  resource,
  kind,
  namespaced: resource !== "nodes" && resource !== "namespaces",
  verbs,
  category: "workloads",
});

function seed() {
  const ready = <T,>(data: T) => ({ status: "ready", data, error: null });
  const cluster = {
    namespace: "default",
    tabs: [{ id: "overview", label: "o" }],
    activeTabId: "overview",
    types: ready({
      types: [
        type("apps", "deployments", "Deployment"),
        type("apps", "daemonsets", "DaemonSet"),
        type("", "pods", "Pod"),
        type("", "nodes", "Node"),
        type("", "namespaces", "Namespace"),
        type("", "events", "Event", ["list"]),
        type("autoscaling", "horizontalpodautoscalers", "HorizontalPodAutoscaler", ["list"]),
      ],
      failedGroups: [],
    }),
  } as unknown as K8sClusterState;
  useK8sStore.setState({
    clusters: { [ASSET]: cluster },
    opDialogs: {},
    terminals: {},
    details: {},
    lists: {},
    objectLists: {},
  });
}

const deployCtx = { assetId: ASSET, typeKey: "apps/deployments", namespace: "default", name: "web" };
const deployment = (replicas = 2) => ({ kind: "Deployment", metadata: { name: "web" }, spec: { replicas } });
const hpa = (name = "api", min = 2, max = 8) => ({
  name,
  object: { spec: { scaleTargetRef: { kind: "Deployment", name: "web" }, minReplicas: min, maxReplicas: max } },
});

let refreshType: Mock<(assetId: number, typeKey: string) => Promise<void>>;
beforeEach(() => {
  vi.clearAllMocks();
  seed();
  refreshType = vi.fn<(assetId: number, typeKey: string) => Promise<void>>().mockResolvedValue(undefined);
  useK8sStore.setState({ refreshType });
  vi.mocked(GetK8sResource).mockResolvedValue(
    JSON.stringify({ notFound: false, object: deployment(), yaml: "" }) as never
  );
  vi.mocked(ListK8sResources).mockResolvedValue(JSON.stringify({ columns: [], rows: [] }) as never);
  vi.mocked(ScaleK8sResource).mockResolvedValue("{}" as never);
  vi.mocked(DeleteK8sResource).mockResolvedValue("{}" as never);
  vi.mocked(RestartK8sWorkload).mockResolvedValue("{}" as never);
});

const open = (kind: "scale" | "restart" | "delete", ctx: object) => {
  render(<K8sOpDialogs assetId={ASSET} />);
  useK8sStore.getState().openOp(kind, ctx as never);
};
const button = (name: RegExp) => screen.getByRole("button", { name });

describe("scale dialog", () => {
  it("shows the current replicas and scales to the typed target through the scale IPC", async () => {
    const user = userEvent.setup();
    open("scale", deployCtx);
    const input = (await screen.findByRole("spinbutton")) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe("2"));
    await user.click(button(/^asset\.k8sScaleIncrease$/));
    await user.click(button(/^asset\.k8sScaleIncrease$/));
    expect(input.value).toBe("4");
    await user.click(button(/asset\.k8sScaleSubmit/));
    await waitFor(() =>
      expect(ScaleK8sResource).toHaveBeenCalledWith({
        ref: expect.objectContaining({ assetId: ASSET, group: "apps", resource: "deployments", name: "web" }),
        replicas: 4,
      })
    );
    await waitFor(() => expect(h.notifySuccess).toHaveBeenCalled());
    expect(refreshType).toHaveBeenCalledWith(ASSET, "apps/deployments");
    await waitFor(() => expect(useK8sStore.getState().opDialogs[ASSET]).toBeUndefined());
  });

  it("warns when an HPA manages the workload but still lets the user submit", async () => {
    vi.mocked(ListK8sResources).mockResolvedValue(
      JSON.stringify({ columns: [], rows: [{ ...hpa(), namespace: "default", object: hpa().object }] }) as never
    );
    const user = userEvent.setup();
    open("scale", deployCtx);
    expect(await screen.findByText("asset.k8sScaleHpa")).toBeInTheDocument();
    await user.click(button(/asset\.k8sScaleSubmit/));
    await waitFor(() => expect(ScaleK8sResource).toHaveBeenCalled());
  });

  it("does not warn when no HPA targets the workload", async () => {
    open("scale", deployCtx);
    await screen.findByRole("spinbutton");
    await waitFor(() => expect(ListK8sResources).toHaveBeenCalled());
    expect(screen.queryByText("asset.k8sScaleHpa")).toBeNull();
  });

  it("rejects a negative target, allows zero", async () => {
    const user = userEvent.setup();
    open("scale", deployCtx);
    const input = (await screen.findByRole("spinbutton")) as HTMLInputElement;
    await waitFor(() => expect(input.value).toBe("2"));
    await user.clear(input);
    await user.type(input, "-1");
    expect(button(/asset\.k8sScaleSubmit/)).toBeDisabled();
    expect(screen.getByText("asset.k8sScaleInvalid")).toBeInTheDocument();
    await user.clear(input);
    await user.type(input, "0");
    expect(button(/asset\.k8sScaleSubmit/)).toBeEnabled();
    await user.click(button(/asset\.k8sScaleSubmit/));
    await waitFor(() => expect(ScaleK8sResource).toHaveBeenCalledWith(expect.objectContaining({ replicas: 0 })));
  });

  it("shows the cluster's reason verbatim and keeps the dialog open on failure", async () => {
    vi.mocked(ScaleK8sResource).mockRejectedValue(new Error("forbidden: cannot patch deployments/scale"));
    const user = userEvent.setup();
    open("scale", deployCtx);
    await waitFor(() => expect(button(/asset\.k8sScaleSubmit/)).toBeEnabled());
    await user.click(button(/asset\.k8sScaleSubmit/));
    await waitFor(() => expect(h.toastError).toHaveBeenCalledWith("forbidden: cannot patch deployments/scale"));
    expect(h.notifySuccess).not.toHaveBeenCalled();
    expect(refreshType).not.toHaveBeenCalled();
    expect(useK8sStore.getState().opDialogs[ASSET]).toBeDefined();
  });
});

describe("restart", () => {
  it("confirms then patches the workload and refreshes", async () => {
    const user = userEvent.setup();
    open("restart", deployCtx);
    await user.click(await screen.findByRole("button", { name: /asset\.k8sRestartConfirm/ }));
    await waitFor(() =>
      expect(RestartK8sWorkload).toHaveBeenCalledWith(expect.objectContaining({ assetId: ASSET, name: "web" }))
    );
    await waitFor(() => expect(h.notifySuccess).toHaveBeenCalled());
    expect(refreshType).toHaveBeenCalledWith(ASSET, "apps/deployments");
  });

  it("toasts the failure and refreshes nothing", async () => {
    vi.mocked(RestartK8sWorkload).mockRejectedValue(new Error("boom"));
    const user = userEvent.setup();
    open("restart", deployCtx);
    await user.click(await screen.findByRole("button", { name: /asset\.k8sRestartConfirm/ }));
    await waitFor(() => expect(h.toastError).toHaveBeenCalledWith("boom"));
    expect(refreshType).not.toHaveBeenCalled();
  });
});

describe("delete", () => {
  const podCtx = { assetId: ASSET, typeKey: "/pods", namespace: "default", name: "worker-1" };
  const pod = (owners: unknown[]) => ({ kind: "Pod", metadata: { name: "worker-1", ownerReferences: owners } });

  it("notes that a controller-owned Pod will be recreated", async () => {
    open("delete", {
      ...podCtx,
      object: pod([{ kind: "ReplicaSet", name: "worker-5c8d", controller: true }]),
    });
    expect(await screen.findByText(/asset\.k8sDeleteControlled/)).toBeInTheDocument();
  });

  it("has no recreate note for a Pod without a controller", async () => {
    open("delete", { ...podCtx, object: pod([]) });
    await screen.findByRole("button", { name: /asset\.k8sDeleteConfirm/ });
    expect(screen.queryByText(/asset\.k8sDeleteControlled/)).toBeNull();
  });

  it("reads the controller from the cached list row when started from a list", async () => {
    useK8sStore.setState({
      lists: {
        [ASSET]: {
          "/pods|default": {
            status: "ready",
            columns: [],
            error: null,
            rows: [
              {
                name: "worker-1",
                namespace: "default",
                ownerReferences: [{ kind: "ReplicaSet", name: "rs", apiVersion: "apps/v1", controller: true }],
                cells: [],
              },
            ],
          },
        },
      } as never,
    });
    open("delete", podCtx);
    expect(await screen.findByText(/asset\.k8sDeleteControlled/)).toBeInTheDocument();
  });

  it("deletes after confirming, closes the resource's detail, log and terminal tabs, then refreshes", async () => {
    const detailId = "res:/pods:default:worker-1";
    useK8sStore.setState({
      clusters: {
        [ASSET]: {
          ...useK8sStore.getState().clusters[ASSET],
          tabs: [
            { id: "overview", label: "o" },
            { id: detailId, label: "worker-1" },
            { id: "res:/pods:default:other", label: "other" },
            { id: "term:1", label: "worker-1 · app" },
            { id: "term:2", label: "other · app" },
          ],
          activeTabId: "term:1",
        },
      },
      logTabs: { [ASSET]: { [detailId]: {} } },
      terminals: {
        [ASSET]: {
          "term:1": { namespace: "default", pod: "worker-1", container: "app" },
          "term:2": { namespace: "default", pod: "other", container: "app" },
        },
      },
    } as never);
    const user = userEvent.setup();
    open("delete", { ...podCtx, object: pod([]) });
    await user.click(await screen.findByRole("button", { name: /asset\.k8sDeleteConfirm/ }));
    await waitFor(() => expect(DeleteK8sResource).toHaveBeenCalledWith(expect.objectContaining({ name: "worker-1" })));
    await waitFor(() =>
      expect(useK8sStore.getState().clusters[ASSET].tabs.map((t) => t.id)).toEqual([
        "overview",
        "res:/pods:default:other",
        "term:2",
      ])
    );
    // The terminal state leaving the store is what ends its exec session.
    expect(Object.keys(useK8sStore.getState().terminals[ASSET])).toEqual(["term:2"]);
    expect(useK8sStore.getState().logTabs[ASSET][detailId]).toBeUndefined();
    expect(h.notifySuccess).toHaveBeenCalled();
    expect(refreshType).toHaveBeenCalledWith(ASSET, "/pods");
  });

  it("keeps every tab and toasts the cluster's reason when the delete fails", async () => {
    vi.mocked(DeleteK8sResource).mockRejectedValue(new Error('pods "worker-1" is forbidden'));
    const detailId = "res:/pods:default:worker-1";
    useK8sStore.setState({
      clusters: {
        [ASSET]: {
          ...useK8sStore.getState().clusters[ASSET],
          tabs: [
            { id: "overview", label: "o" },
            { id: detailId, label: "worker-1" },
          ],
        },
      },
    } as never);
    const user = userEvent.setup();
    open("delete", { ...podCtx, object: pod([]) });
    await user.click(await screen.findByRole("button", { name: /asset\.k8sDeleteConfirm/ }));
    await waitFor(() => expect(h.toastError).toHaveBeenCalledWith('pods "worker-1" is forbidden'));
    expect(useK8sStore.getState().clusters[ASSET].tabs.map((t) => t.id)).toContain(detailId);
    expect(h.notifySuccess).not.toHaveBeenCalled();
    expect(refreshType).not.toHaveBeenCalled();
  });

  it("requires typing the namespace name before a Namespace can be deleted", async () => {
    const user = userEvent.setup();
    open("delete", { assetId: ASSET, typeKey: "/namespaces", namespace: "", name: "team-a" });
    const confirm = await screen.findByRole("button", { name: /asset\.k8sDeleteConfirm/ });
    expect(confirm).toBeDisabled();
    const input = screen.getByRole("textbox");
    await user.type(input, "team-");
    expect(confirm).toBeDisabled();
    await user.clear(input);
    await user.type(input, "team-a");
    expect(confirm).toBeEnabled();
    await user.click(confirm);
    await waitFor(() =>
      expect(DeleteK8sResource).toHaveBeenCalledWith(
        expect.objectContaining({ resource: "namespaces", name: "team-a" })
      )
    );
  });
});

describe("actions per type", () => {
  const row = { name: "x", namespace: "default", cells: [] } as unknown as K8sResourceRow;
  const ids = (typeKey: string) => getK8sActions(typeKey, row, ASSET).map((a) => a.id);

  it("offers scale + restart + delete on Deployments, restart + delete on DaemonSets, delete on Pods", () => {
    expect(ids("apps/deployments")).toEqual(expect.arrayContaining(["scale", "restart", "delete"]));
    expect(ids("apps/daemonsets")).toEqual(expect.arrayContaining(["restart", "delete"]));
    expect(ids("apps/daemonsets")).not.toContain("scale");
    expect(ids("/pods")).toContain("delete");
    expect(ids("/pods")).not.toContain("scale");
  });

  it("offers no delete on Nodes, nor on types whose verbs lack delete", () => {
    expect(ids("/nodes")).not.toContain("delete");
    expect(ids("/events")).not.toContain("delete");
  });

  it("draws delete as the last, destructive action", () => {
    const all = getK8sActions("apps/deployments", row, ASSET);
    expect(all[all.length - 1].id).toBe("delete");
    expect(all[all.length - 1].destructive).toBe(true);
  });
});
