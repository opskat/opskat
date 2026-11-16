import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Play } from "lucide-react";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { registerK8sAction } from "@/components/k8s/actions";
import { useK8sStore } from "@/stores/k8sStore";
import { GetK8sClusterVersion, GetK8sResourceTypes, ListK8sResources } from "../../wailsjs/go/k8s/K8s";
import type { asset_entity, k8s } from "../../wailsjs/go/models";

const copied = vi.hoisted(() => vi.fn());
vi.mock("@/lib/notify", () => ({ notifyCopied: copied, notifySuccess: vi.fn() }));

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

interface PodSpec {
  name: string;
  status?: string;
  phase?: string;
  created?: string;
  age?: string;
}

function podTable(pods: PodSpec[]) {
  return JSON.stringify({
    columns: [
      { name: "Name", type: "string", format: "name" },
      { name: "Status", type: "string", format: "" },
      { name: "Age", type: "string", format: "" },
    ],
    rows: pods.map((p) => ({
      name: p.name,
      namespace: "default",
      uid: p.name,
      creationTimestamp: p.created ?? "2026-11-16T00:00:00Z",
      labels: {},
      ownerReferences: [],
      cells: [p.name, p.status ?? "Running", p.age ?? "1m"],
      phase: p.phase ?? "Running",
    })),
  });
}

let nextId = 91000;
const asset = (ID: number) =>
  ({ ID, Name: "k8s", Type: "k8s", Config: '{"namespace":"default"}' }) as asset_entity.Asset;

function mockPods(pods: (req: k8s.K8sListRequest) => string | Promise<string>) {
  vi.mocked(GetK8sClusterVersion).mockResolvedValue(JSON.stringify({ version: "v1", platform: "x" }) as never);
  vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(types) as never);
  vi.mocked(ListK8sResources).mockImplementation((async (req: k8s.K8sListRequest) => {
    if (req.resource === "namespaces") return podTable([{ name: "default" }]);
    return pods(req);
  }) as never);
}

const podCalls = () =>
  vi
    .mocked(ListK8sResources)
    .mock.calls.map(([r]) => r)
    .filter((r) => r.resource === "pods");
const rowNames = () => screen.queryAllByTestId("k8s-row").map((r) => r.firstElementChild?.textContent);

async function openPods(user: ReturnType<typeof userEvent.setup>) {
  const sidebar = await screen.findByTestId("k8s-sidebar");
  await user.click(await within(sidebar).findByText("Pod"));
}

describe("K8s resource list", () => {
  beforeEach(() => {
    vi.mocked(GetK8sClusterVersion).mockReset();
    vi.mocked(GetK8sResourceTypes).mockReset();
    vi.mocked(ListK8sResources).mockReset();
    copied.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("opens the apply YAML dialog from the toolbar button", async () => {
    const user = userEvent.setup();
    mockPods(() => podTable([{ name: "a" }]));
    const id = nextId++;
    render(<K8sClusterPage asset={asset(id)} />);
    await openPods(user);
    await screen.findByText("a");

    await user.click(screen.getByRole("button", { name: "asset.k8sApplyYaml" }));
    expect(useK8sStore.getState().yamlDialogs[id]).toEqual({ mode: "apply" });
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("sorts by name ascending by default and toggles direction on header click", async () => {
    const user = userEvent.setup();
    mockPods(() => podTable([{ name: "b" }, { name: "c" }, { name: "a" }]));
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("a");
    expect(rowNames()).toEqual(["a", "b", "c"]);

    await user.click(screen.getByRole("button", { name: "Name" }));
    expect(rowNames()).toEqual(["c", "b", "a"]);
  });

  it("sorts the age column by creation timestamp, not by the age text", async () => {
    const user = userEvent.setup();
    mockPods(() =>
      podTable([
        { name: "mid", created: "2026-11-10T00:00:00Z", age: "6d" },
        { name: "old", created: "2026-10-01T00:00:00Z", age: "46d" },
        { name: "new", created: "2026-11-16T00:00:00Z", age: "5m" },
      ])
    );
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("mid");

    await user.click(screen.getByRole("button", { name: "Age" }));
    expect(rowNames()).toEqual(["old", "mid", "new"]);
    await user.click(screen.getByRole("button", { name: "Age" }));
    expect(rowNames()).toEqual(["new", "mid", "old"]);
  });

  it("filters by name case-insensitively without asking the cluster", async () => {
    const user = userEvent.setup();
    mockPods(() => podTable([{ name: "api-1" }, { name: "Web-1" }, { name: "api-2" }]));
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("Web-1");

    await user.type(screen.getByPlaceholderText("asset.k8sFilterPlaceholder"), "WEB");
    expect(rowNames()).toEqual(["Web-1"]);
    expect(podCalls()).toHaveLength(1);
    expect(podCalls()[0].labelSelector).toBe("");
  });

  it("sends a filter containing '=' as a label selector", async () => {
    const user = userEvent.setup();
    mockPods((req) => podTable(req.labelSelector ? [{ name: "api-1" }] : [{ name: "api-1" }, { name: "web-1" }]));
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("web-1");

    await user.type(screen.getByPlaceholderText("asset.k8sFilterPlaceholder"), "app=api,tier!=db");
    await waitFor(() => expect(podCalls().at(-1)?.labelSelector).toBe("app=api,tier!=db"));
    await waitFor(() => expect(rowNames()).toEqual(["api-1"]));
  });

  it("shows the cluster's selector error under the box and keeps the previous rows", async () => {
    const user = userEvent.setup();
    mockPods((req) => {
      if (req.labelSelector) throw new Error("unable to parse requirement: found '=', expected: identifier");
      return podTable([{ name: "api-1" }, { name: "web-1" }]);
    });
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("web-1");

    await user.type(screen.getByPlaceholderText("asset.k8sFilterPlaceholder"), "=bad");
    expect(await screen.findByText(/unable to parse requirement/)).toBeInTheDocument();
    expect(rowNames()).toEqual(["api-1", "web-1"]);
    expect(screen.queryByTestId("async-state-error")).not.toBeInTheDocument();
  });

  it("keeps filter, sort and auto-refresh when the page unmounts and remounts", async () => {
    const user = userEvent.setup();
    mockPods(() => podTable([{ name: "api-1" }, { name: "web-1" }]));
    const a = asset(nextId++);
    const first = render(<K8sClusterPage asset={a} />);
    await openPods(user);
    await screen.findByText("web-1");
    await user.type(screen.getByPlaceholderText("asset.k8sFilterPlaceholder"), "web");
    await user.click(screen.getByRole("button", { name: "Name" }));
    await user.click(screen.getByRole("switch", { name: "asset.k8sAutoRefresh" }));
    first.unmount();

    render(<K8sClusterPage asset={a} />);
    expect(await screen.findByPlaceholderText("asset.k8sFilterPlaceholder")).toHaveValue("web");
    expect(screen.getByRole("switch", { name: "asset.k8sAutoRefresh" })).toBeChecked();
    expect(rowNames()).toEqual(["web-1"]);
  });

  it("auto-refreshes every 5s only while the list tab is active and the page mounted", async () => {
    const a = asset(nextId++);
    mockPods(() => podTable([{ name: "api-1" }]));
    const view = render(<K8sClusterPage asset={a} />);
    const sidebar = await screen.findByTestId("k8s-sidebar");
    fireEvent.click(await within(sidebar).findByText("Pod"));
    await screen.findByText("api-1");
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("switch", { name: "asset.k8sAutoRefresh" }));
    expect(podCalls()).toHaveLength(1);

    await act(async () => void (await vi.advanceTimersByTimeAsync(4900)));
    expect(podCalls()).toHaveLength(1);
    await act(async () => void (await vi.advanceTimersByTimeAsync(200)));
    expect(podCalls()).toHaveLength(2);
    await act(async () => void (await vi.advanceTimersByTimeAsync(5000)));
    expect(podCalls()).toHaveLength(3);

    // another inner tab becomes active: paused
    act(() => useK8sStore.getState().setActiveTab(a.ID, "overview"));
    await act(async () => void (await vi.advanceTimersByTimeAsync(20000)));
    expect(podCalls()).toHaveLength(3);

    // back on the list tab: resumes; unmounting the page stops it again
    act(() => useK8sStore.getState().setActiveTab(a.ID, "list:/pods"));
    await act(async () => void (await vi.advanceTimersByTimeAsync(5100)));
    expect(podCalls()).toHaveLength(4);
    view.unmount();
    await vi.advanceTimersByTimeAsync(20000);
    expect(podCalls()).toHaveLength(4);
  });

  it("colours status cells by meaning", async () => {
    const user = userEvent.setup();
    mockPods(() =>
      podTable([
        { name: "a", status: "Running" },
        { name: "b", status: "Pending" },
        { name: "c", status: "CrashLoopBackOff" },
        { name: "d", status: "Mystery" },
      ])
    );
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("Running");
    expect(screen.getByText("Running").className).toContain("text-success");
    expect(screen.getByText("Pending").className).toContain("text-warning");
    expect(screen.getByText("CrashLoopBackOff").className).toContain("text-destructive");
    expect(screen.getByText("Mystery").className).toContain("text-muted-foreground");
  });

  it("virtualises a 5000 row list", async () => {
    const user = userEvent.setup();
    const pods = Array.from({ length: 5000 }, (_, i) => ({ name: `pod-${String(i).padStart(4, "0")}` }));
    mockPods(() => podTable(pods));
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("pod-0000");
    const rendered = screen.getAllByTestId("k8s-row").length;
    expect(rendered).toBeGreaterThan(0);
    expect(rendered).toBeLessThan(500);
    expect(screen.getByText("5000")).toBeInTheDocument();
  });

  it("opens the detail tab when a row is clicked", async () => {
    const user = userEvent.setup();
    const a = asset(nextId++);
    mockPods(() => podTable([{ name: "api-1" }]));
    render(<K8sClusterPage asset={a} />);
    await openPods(user);
    await user.click(await screen.findByText("api-1"));
    expect(useK8sStore.getState().clusters[a.ID].activeTabId).toBe("res:/pods:default:api-1");
  });

  it("shows registered actions in hover icons and the ⋯ menu", async () => {
    const user = userEvent.setup();
    const run = vi.fn();
    mockPods(() => podTable([{ name: "api-1" }]));
    const a = asset(nextId++);
    render(<K8sClusterPage asset={a} />);
    await openPods(user);
    await screen.findByText("api-1");

    const off = registerK8sAction({
      id: "t.primary",
      labelKey: "test.primary",
      icon: Play,
      primary: true,
      appliesTo: (typeKey) => typeKey === "/pods",
      run,
    });
    const off2 = registerK8sAction({
      id: "t.other",
      labelKey: "test.other",
      icon: Play,
      appliesTo: (typeKey) => typeKey === "/pods",
      run: vi.fn(),
    });
    try {
      // re-render the list tab so the registry is consulted again
      act(() => useK8sStore.getState().setActiveTab(a.ID, "overview"));
      act(() => useK8sStore.getState().setActiveTab(a.ID, "list:/pods"));
      await user.click(await screen.findByRole("button", { name: "test.primary" }));
      expect(run).toHaveBeenCalledWith(
        expect.objectContaining({ assetId: a.ID, typeKey: "/pods", namespace: "default", name: "api-1" })
      );
      expect(useK8sStore.getState().clusters[a.ID].activeTabId).toBe("list:/pods");

      await user.click(screen.getByRole("button", { name: "asset.k8sMoreActions" }));
      expect(await screen.findByRole("menuitem", { name: "test.other" })).toBeInTheDocument();
    } finally {
      off();
      off2();
    }
  });

  it("greys a row's terminal icon by the Pod phase, not the Status column", async () => {
    const user = userEvent.setup();
    mockPods(() =>
      podTable([
        { name: "api-1", status: "Running" },
        { name: "job-1", status: "Completed", phase: "Succeeded" },
        { name: "crash-1", status: "CrashLoopBackOff" }, // a Running Pod whose container keeps failing
      ])
    );
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openPods(user);
    await screen.findByText("job-1");
    const terminalOf = (name: string) =>
      within(screen.getByText(name).closest<HTMLElement>('[data-testid="k8s-row"]')!).getByRole("button", {
        name: "asset.k8sTerminal",
      });
    expect(terminalOf("api-1")).toBeEnabled();
    expect(terminalOf("job-1")).toBeDisabled();
    expect(terminalOf("crash-1")).toBeEnabled();
  });

  it("offers Open detail and Copy name on right click", async () => {
    const user = userEvent.setup();
    const a = asset(nextId++);
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    mockPods(() => podTable([{ name: "api-1" }]));
    render(<K8sClusterPage asset={a} />);
    await openPods(user);
    const row = (await screen.findByTestId("k8s-row")) as HTMLElement;

    fireEvent.contextMenu(row);
    fireEvent.click(await screen.findByRole("menuitem", { name: "asset.k8sCopyName" }));
    expect(writeText).toHaveBeenCalledWith("api-1");
    await waitFor(() => expect(copied).toHaveBeenCalled());

    fireEvent.contextMenu(row);
    fireEvent.click(await screen.findByRole("menuitem", { name: "asset.k8sOpenDetail" }));
    expect(useK8sStore.getState().clusters[a.ID].activeTabId).toBe("res:/pods:default:api-1");
  });
});
