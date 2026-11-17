import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { getK8sActions } from "@/components/k8s/actions";
import { K8sOpDialogs } from "@/components/k8s/ops/K8sOpDialogs";
import "@/components/k8s/ops/register";
import { useK8sStore, type K8sClusterState, type K8sResourceRow, type K8sTableColumn } from "@/stores/k8sStore";
import { SetK8sCronJobSuspend, SetK8sNodeUnschedulable, TriggerK8sCronJob } from "../../wailsjs/go/k8s/K8s";

const h = vi.hoisted(() => ({ notifySuccess: vi.fn(), toastError: vi.fn() }));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: h.notifySuccess }));
vi.mock("sonner", () => ({ toast: { error: h.toastError, success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

const ASSET = 95000;
const type = (group: string, resource: string, kind: string, namespaced = true) => ({
  group,
  version: "v1",
  resource,
  kind,
  namespaced,
  verbs: ["list", "delete", "patch", "create"],
  category: "workloads",
});

const cronColumns: K8sTableColumn[] = [
  { name: "Name", type: "string", format: "name" },
  { name: "Schedule", type: "string", format: "" },
  { name: "Suspend", type: "boolean", format: "" },
];
const nodeColumns: K8sTableColumn[] = [
  { name: "Name", type: "string", format: "name" },
  { name: "Status", type: "string", format: "" },
];
const row = (name: string, cells: unknown[], namespace = ""): K8sResourceRow => ({
  name,
  namespace,
  uid: name,
  creationTimestamp: "2026-11-16T00:00:00Z",
  labels: {},
  ownerReferences: [],
  cells,
});
const cron = (suspend: boolean) => row("nightly", ["nightly", "0 2 * * *", suspend], "default");
const node = (status: string) => row("n1", ["n1", status]);

const listEntry = (columns: K8sTableColumn[], rows: K8sResourceRow[]) => ({
  status: "ready" as const,
  columns,
  rows,
  error: null,
  reloading: false,
  filterError: null,
});

function seed(cronRow: K8sResourceRow = cron(false), nodeRow: K8sResourceRow = node("Ready")) {
  const ready = <T,>(data: T) => ({ status: "ready", data, error: null });
  const cluster = {
    namespace: "default",
    tabs: [{ id: "overview", label: "o" }],
    activeTabId: "overview",
    types: ready({
      types: [type("batch", "cronjobs", "CronJob"), type("batch", "jobs", "Job"), type("", "nodes", "Node", false)],
      failedGroups: [],
    }),
  } as unknown as K8sClusterState;
  useK8sStore.setState({
    clusters: { [ASSET]: cluster },
    opDialogs: {},
    terminals: {},
    details: {},
    lists: {
      [ASSET]: {
        "batch/cronjobs|default": listEntry(cronColumns, [cronRow]),
        "/nodes|": listEntry(nodeColumns, [nodeRow]),
      },
    },
  });
}

const cronCtx = { assetId: ASSET, typeKey: "batch/cronjobs", namespace: "default", name: "nightly" };
const nodeCtx = { assetId: ASSET, typeKey: "/nodes", namespace: "", name: "n1" };
const action = (id: string, typeKey: string, target: K8sResourceRow, columns: K8sTableColumn[]) => {
  const a = getK8sActions(typeKey, target, ASSET).find((x) => x.id === id);
  return { a, labelKey: a && (a.labelKeyFor?.(target, columns) ?? a.labelKey) };
};

let refreshType: Mock<(assetId: number, typeKey: string) => Promise<void>>;
beforeEach(() => {
  vi.clearAllMocks();
  seed();
  refreshType = vi.fn<(assetId: number, typeKey: string) => Promise<void>>().mockResolvedValue(undefined);
  useK8sStore.setState({ refreshType });
  vi.mocked(TriggerK8sCronJob).mockResolvedValue(JSON.stringify({ jobName: "nightly-manual-ab12c" }) as never);
  vi.mocked(SetK8sCronJobSuspend).mockResolvedValue("{}" as never);
  vi.mocked(SetK8sNodeUnschedulable).mockResolvedValue("{}" as never);
});

const button = (name: RegExp) => screen.getByRole("button", { name });

describe("action availability and labels", () => {
  it("CronJob offers run-now and a pause/resume label that follows the Suspend column", () => {
    expect(action("cron-trigger", "batch/cronjobs", cron(false), cronColumns).a).toBeDefined();
    expect(action("cron-suspend", "batch/cronjobs", cron(false), cronColumns).labelKey).toBe("asset.k8sCronSuspend");
    expect(action("cron-suspend", "batch/cronjobs", cron(true), cronColumns).labelKey).toBe("asset.k8sCronResume");
    expect(action("cron-trigger", "apps/deployments", cron(false), cronColumns).a).toBeUndefined();
  });

  it("reads the state from the full object on the detail view", () => {
    const a = getK8sActions("batch/cronjobs", { spec: { suspend: true } }, ASSET).find((x) => x.id === "cron-suspend");
    expect(a?.labelKeyFor?.({ spec: { suspend: true } })).toBe("asset.k8sCronResume");
    const n = getK8sActions("/nodes", { spec: { unschedulable: true } }, ASSET).find((x) => x.id === "node-cordon");
    expect(n?.labelKeyFor?.({ spec: { unschedulable: true } })).toBe("asset.k8sUncordonAction");
  });

  it("node label follows SchedulingDisabled in the status column", () => {
    expect(action("node-cordon", "/nodes", node("Ready"), nodeColumns).labelKey).toBe("asset.k8sCordonAction");
    expect(action("node-cordon", "/nodes", node("Ready,SchedulingDisabled"), nodeColumns).labelKey).toBe(
      "asset.k8sUncordonAction"
    );
  });

  it("run-now is a primary row action", () => {
    expect(action("cron-trigger", "batch/cronjobs", cron(false), cronColumns).a?.primary).toBe(true);
  });
});

describe("run now", () => {
  const open = () => {
    render(<K8sOpDialogs assetId={ASSET} />);
    useK8sStore.getState().openOp("trigger", cronCtx);
  };

  it("creates the Job, toasts with a clickable link to it and refreshes the lists", async () => {
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: /asset\.k8sCronTriggerConfirm/ }));
    await waitFor(() =>
      expect(TriggerK8sCronJob).toHaveBeenCalledWith({ assetId: ASSET, namespace: "default", name: "nightly" })
    );
    await waitFor(() => expect(h.notifySuccess).toHaveBeenCalled());
    const [message, options] = h.notifySuccess.mock.calls[0];
    expect(message).toContain("nightly-manual-ab12c");
    options.action.onClick();
    const tabs = useK8sStore.getState().clusters[ASSET].tabs;
    expect(tabs.at(-1)).toEqual({ id: "res:batch/jobs:default:nightly-manual-ab12c", label: "nightly-manual-ab12c" });
    expect(useK8sStore.getState().clusters[ASSET].activeTabId).toBe("res:batch/jobs:default:nightly-manual-ab12c");
    expect(refreshType).toHaveBeenCalledWith(ASSET, "batch/jobs");
    expect(refreshType).toHaveBeenCalledWith(ASSET, "batch/cronjobs");
    expect(useK8sStore.getState().opDialogs[ASSET]).toBeUndefined();
  });

  it("shows the cluster's reason and refreshes nothing on failure", async () => {
    vi.mocked(TriggerK8sCronJob).mockRejectedValue(new Error('jobs.batch is forbidden: cannot create resource "jobs"'));
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: /asset\.k8sCronTriggerConfirm/ }));
    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('jobs.batch is forbidden: cannot create resource "jobs"')
    );
    expect(h.notifySuccess).not.toHaveBeenCalled();
    expect(refreshType).not.toHaveBeenCalled();
  });
});

describe("pause / resume", () => {
  it("suspends an active CronJob, resumes a suspended one, then refreshes the list", async () => {
    const run = (r: K8sResourceRow) => {
      seed(r);
      return getK8sActions("batch/cronjobs", r, ASSET)
        .find((x) => x.id === "cron-suspend")!
        .run(cronCtx);
    };
    await run(cron(false));
    expect(SetK8sCronJobSuspend).toHaveBeenLastCalledWith({
      assetId: ASSET,
      namespace: "default",
      name: "nightly",
      suspend: true,
    });
    await waitFor(() => expect(refreshType).toHaveBeenCalledWith(ASSET, "batch/cronjobs"));
    await run(cron(true));
    expect(SetK8sCronJobSuspend).toHaveBeenLastCalledWith({
      assetId: ASSET,
      namespace: "default",
      name: "nightly",
      suspend: false,
    });
    expect(h.notifySuccess).toHaveBeenCalledTimes(2);
  });

  it("keeps state and reports the cluster's reason on failure", async () => {
    vi.mocked(SetK8sCronJobSuspend).mockRejectedValue(new Error("forbidden: cannot patch cronjobs"));
    await getK8sActions("batch/cronjobs", cron(false), ASSET)
      .find((x) => x.id === "cron-suspend")!
      .run(cronCtx);
    expect(h.toastError).toHaveBeenCalledWith("forbidden: cannot patch cronjobs");
    expect(refreshType).not.toHaveBeenCalled();
    expect(h.notifySuccess).not.toHaveBeenCalled();
  });
});

describe("cordon", () => {
  const open = (ctx = nodeCtx) => {
    render(<K8sOpDialogs assetId={ASSET} />);
    useK8sStore.getState().openOp("cordon", ctx);
  };

  it("states that nothing is evicted, then cordons through the node IPC", async () => {
    const user = userEvent.setup();
    open();
    expect(await screen.findByText("asset.k8sCordonDesc")).toBeInTheDocument();
    await user.click(button(/^asset\.k8sCordonConfirm$/));
    await waitFor(() =>
      expect(SetK8sNodeUnschedulable).toHaveBeenCalledWith({ assetId: ASSET, name: "n1", unschedulable: true })
    );
    await waitFor(() => expect(refreshType).toHaveBeenCalledWith(ASSET, "/nodes"));
    expect(h.notifySuccess).toHaveBeenCalled();
  });

  it("uncordons a node already marked SchedulingDisabled", async () => {
    seed(cron(false), node("Ready,SchedulingDisabled"));
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: /^asset\.k8sUncordonConfirm$/ }));
    await waitFor(() =>
      expect(SetK8sNodeUnschedulable).toHaveBeenCalledWith({ assetId: ASSET, name: "n1", unschedulable: false })
    );
  });

  it("reports the cluster's reason and refreshes nothing on failure", async () => {
    vi.mocked(SetK8sNodeUnschedulable).mockRejectedValue(new Error("forbidden: cannot patch nodes"));
    const user = userEvent.setup();
    open();
    await user.click(await screen.findByRole("button", { name: /^asset\.k8sCordonConfirm$/ }));
    await waitFor(() => expect(h.toastError).toHaveBeenCalledWith("forbidden: cannot patch nodes"));
    expect(refreshType).not.toHaveBeenCalled();
  });
});
