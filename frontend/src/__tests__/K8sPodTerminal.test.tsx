import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { getK8sActions } from "@/components/k8s/actions";
import { openPodTerminal } from "@/components/k8s/terminal/register";
import { useK8sStore, type K8sResourceRow } from "@/stores/k8sStore";
import { useTabStore } from "@/stores/tabStore";
import { toXtermTheme, useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { defaultLightTheme } from "@/data/terminalThemes";
import {
  GetK8sClusterVersion,
  GetK8sResource,
  GetK8sResourceTypes,
  ListK8sResources,
  ResizeK8sExec,
  StartK8sExec,
  StopK8sExec,
  WriteK8sExec,
} from "../../wailsjs/go/k8s/K8s";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import type { asset_entity } from "../../wailsjs/go/models";

const xterm = vi.hoisted(() => ({
  instances: [] as {
    options: Record<string, unknown>;
    write: ReturnType<typeof vi.fn>;
    dispose: ReturnType<typeof vi.fn>;
    onDataHandler?: (data: string) => void;
    ctorOptions: Record<string, unknown>;
  }[],
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 80;
    rows = 24;
    options: Record<string, unknown>;
    ctorOptions: Record<string, unknown>;
    write = vi.fn();
    dispose = vi.fn();
    onDataHandler?: (data: string) => void;
    constructor(options: Record<string, unknown>) {
      this.ctorOptions = options;
      this.options = { ...options };
      xterm.instances.push(this);
    }
    loadAddon() {}
    open(el: HTMLElement) {
      el.appendChild(document.createElement("div")).className = "xterm";
    }
    onData(handler: (data: string) => void) {
      this.onDataHandler = handler;
      return { dispose: vi.fn() };
    }
    focus() {}
    getSelection() {
      return "";
    }
    paste() {}
    selectAll() {}
  },
}));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit = vi.fn();
  },
}));
vi.mock("@xterm/addon-search", () => ({
  SearchAddon: class {
    clearDecorations = vi.fn();
    findNext = vi.fn();
    findPrevious = vi.fn();
    onDidChangeResults = vi.fn(() => ({ dispose: vi.fn() }));
  },
}));
vi.mock("@/components/terminal/terminalInputBridge", () => ({
  createTerminalInputBridge: () => ({
    setShortcuts: vi.fn(),
    setOnCopy: vi.fn(),
    setOnPaste: vi.fn(),
    setOnSelectAll: vi.fn(),
    setOnFind: vi.fn(),
    dispose: vi.fn(),
  }),
}));

const handlers = new Map<string, (payload: unknown) => void>();

const pod = (name: string, extra: { phase?: string; annotations?: Record<string, string> } = {}) => ({
  apiVersion: "v1",
  kind: "Pod",
  metadata: { name, namespace: "prod", uid: `uid-${name}`, annotations: extra.annotations ?? {} },
  spec: {
    initContainers: [{ name: "init-db", image: "busybox" }],
    containers: [
      { name: "api", image: "example/api:1" },
      { name: "istio-proxy", image: "example/proxy:1" },
    ],
  },
  status: { phase: extra.phase ?? "Running" },
});

let nextId = 97000;
const asset = (ID: number) => ({ ID, Name: "k8s", Type: "k8s", Config: "{}" }) as asset_entity.Asset;

/** A cluster with state in the store, as if its page had loaded. */
function cluster() {
  const id = nextId++;
  useK8sStore.getState().initCluster(id, "prod");
  return id;
}

const lastTerminal = () => xterm.instances[xterm.instances.length - 1];

function activeTerminalTab(assetId: number) {
  const c = useK8sStore.getState().clusters[assetId];
  return c.tabs.find((t) => t.id === c.activeTabId)!;
}

async function openTerminal(assetId: number, object: Record<string, unknown>, container?: string) {
  await act(() => openPodTerminal({ assetId, typeKey: "/pods", namespace: "prod", name: "api-0", object, container }));
  return activeTerminalTab(assetId).id;
}

describe("Pod terminal", () => {
  beforeEach(() => {
    xterm.instances.length = 0;
    handlers.clear();
    vi.mocked(GetK8sClusterVersion).mockResolvedValue(JSON.stringify({ version: "v1", platform: "x" }) as never);
    vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify({ types: [], failedGroups: [] }) as never);
    vi.mocked(ListK8sResources).mockResolvedValue(JSON.stringify({ columns: [], rows: [] }) as never);
    vi.mocked(StartK8sExec).mockReset();
    vi.mocked(StartK8sExec).mockResolvedValue("sess-1" as never);
    vi.mocked(StopK8sExec).mockReset();
    vi.mocked(StopK8sExec).mockResolvedValue(undefined as never);
    vi.mocked(WriteK8sExec).mockReset();
    vi.mocked(WriteK8sExec).mockResolvedValue(undefined as never);
    vi.mocked(ResizeK8sExec).mockResolvedValue(undefined as never);
    vi.mocked(EventsOff).mockClear();
    vi.mocked(EventsOn).mockImplementation(((event: string, handler: (payload: unknown) => void) => {
      handlers.set(event, handler);
      return () => handlers.delete(event);
    }) as never);
  });

  it("is greyed with the reason while the Pod is not running, on objects and on list rows", () => {
    const action = (target: Record<string, unknown> | K8sResourceRow) =>
      getK8sActions("/pods", target).find((a) => a.id === "terminal")!;
    const running = pod("a");
    expect(action(running).primary).toBe(true);
    expect(action(running).disabledReason?.(running)).toBeUndefined();
    expect(action(pod("b", { phase: "Pending" })).disabledReason?.(pod("b", { phase: "Pending" }))).toMatch(/Pending/);

    const columns = [
      { name: "Name", type: "string", format: "name" },
      { name: "Status", type: "string", format: "" },
    ];
    const row = (status: string): K8sResourceRow => ({
      name: "a",
      namespace: "prod",
      uid: "u",
      creationTimestamp: "",
      labels: {},
      ownerReferences: [],
      cells: ["a", status],
    });
    expect(action(row("Running")).disabledReason?.(row("Running"), columns)).toBeUndefined();
    expect(action(row("Completed")).disabledReason?.(row("Completed"), columns)).toMatch(/Completed/);
    expect(getK8sActions("apps/deployments", running).some((a) => a.id === "terminal")).toBe(false);
  });

  it("opens a new tab per open, in the annotated default container, with the app terminal font", async () => {
    useTerminalThemeStore.setState({ fontSize: 17, scrollback: 4321 });
    const id = cluster();
    const annotated = pod("api-0", { annotations: { "kubectl.kubernetes.io/default-container": "istio-proxy" } });

    const first = await openTerminal(id, annotated);
    expect(activeTerminalTab(id).label).toBe("api-0 · istio-proxy");
    expect(StartK8sExec).toHaveBeenCalledWith({
      assetId: id,
      namespace: "prod",
      pod: "api-0",
      container: "istio-proxy",
      shell: "auto",
      cols: 80,
      rows: 24,
    });
    expect(lastTerminal().ctorOptions).toMatchObject({ fontSize: 17, scrollback: 4321 });
    expect(useK8sStore.getState().terminals[id][first].containers).toEqual(["api", "istio-proxy"]);

    const second = await openTerminal(id, pod("api-0"));
    expect(second).not.toBe(first);
    expect(activeTerminalTab(id).label).toBe("api-0 · api");

    await openTerminal(id, annotated, "api");
    expect(activeTerminalTab(id).label).toBe("api-0 · api");
    expect(useK8sStore.getState().clusters[id].tabs.filter((t) => t.id.startsWith("term:"))).toHaveLength(3);
  });

  it("reads the Pod first when opened from a list row, and refuses one that stopped running", async () => {
    const id = cluster();
    vi.mocked(GetK8sResource).mockResolvedValueOnce(
      JSON.stringify({ notFound: false, object: pod("api-0"), yaml: "" }) as never
    );
    await act(() => openPodTerminal({ assetId: id, typeKey: "/pods", namespace: "prod", name: "api-0" }));
    expect(GetK8sResource).toHaveBeenCalledWith(
      expect.objectContaining({
        assetId: id,
        group: "",
        version: "v1",
        resource: "pods",
        namespace: "prod",
        name: "api-0",
      })
    );
    expect(activeTerminalTab(id).label).toBe("api-0 · api");

    vi.mocked(GetK8sResource).mockResolvedValueOnce(
      JSON.stringify({ notFound: false, object: pod("api-0", { phase: "Succeeded" }), yaml: "" }) as never
    );
    vi.mocked(StartK8sExec).mockClear();
    await act(() => openPodTerminal({ assetId: id, typeKey: "/pods", namespace: "prod", name: "api-0" }));
    expect(StartK8sExec).not.toHaveBeenCalled();
  });

  it("keeps the xterm and session across inner-tab switches and the page unmounting", async () => {
    const user = userEvent.setup();
    const id = nextId++;
    const view = render(<K8sClusterPage asset={asset(id)} />);
    await screen.findByTestId("k8s-sidebar");
    const tabId = await openTerminal(id, pod("api-0"));
    await waitFor(() => expect(useK8sStore.getState().terminals[id][tabId].status).toBe("connected"));
    const term = lastTerminal();

    const host = screen.getByTestId("k8s-pod-terminal").querySelector(".xterm")!.parentElement!;
    expect(screen.getByText("prod/api-0")).toBeInTheDocument();
    expect(screen.getByText("asset.k8sTerminalConnected")).toBeInTheDocument();
    expect(term.options.theme).toEqual(toXtermTheme(defaultLightTheme));

    act(() => handlers.get("k8s:exec:data:sess-1")!(btoa("root@api-0:/# ")));
    expect(term.write).toHaveBeenCalledWith(new TextEncoder().encode("root@api-0:/# "));
    term.onDataHandler!("ls\r");
    await waitFor(() => expect(WriteK8sExec).toHaveBeenCalledWith("sess-1", btoa("ls\r")));

    // another inner tab, then back
    act(() => useK8sStore.getState().setActiveTab(id, "overview"));
    expect(screen.queryByTestId("k8s-pod-terminal")).not.toBeInTheDocument();
    act(() => useK8sStore.getState().setActiveTab(id, tabId));
    expect(screen.getByTestId("k8s-pod-terminal").contains(host)).toBe(true);

    // a top-level tab switch unmounts the whole page
    view.unmount();
    expect(host.isConnected).toBe(false);
    render(<K8sClusterPage asset={asset(id)} />);
    expect((await screen.findByTestId("k8s-pod-terminal")).contains(host)).toBe(true);

    expect(xterm.instances).toHaveLength(1);
    expect(term.dispose).not.toHaveBeenCalled();
    expect(StopK8sExec).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "ssh.contextMenu.find" }));
    expect(await screen.findByRole("textbox")).toBeInTheDocument();
  });

  it("ends the session when its tab closes, and all of a cluster's when the cluster tab closes", async () => {
    const a = cluster();
    const b = cluster();
    vi.mocked(StartK8sExec)
      .mockResolvedValueOnce("sess-a1" as never)
      .mockResolvedValueOnce("sess-a2" as never)
      .mockResolvedValueOnce("sess-b1" as never);
    const a1 = await openTerminal(a, pod("api-0"));
    const termA1 = lastTerminal();
    await openTerminal(a, pod("api-0"));
    const termA2 = lastTerminal();
    await openTerminal(b, pod("api-0"));
    await waitFor(() => expect(handlers.has("k8s:exec:end:sess-b1")).toBe(true));

    act(() => useK8sStore.getState().closeTabs(a, [a1]));
    expect(StopK8sExec).toHaveBeenCalledWith("sess-a1");
    expect(EventsOff).toHaveBeenCalledWith("k8s:exec:data:sess-a1");
    expect(termA1.dispose).toHaveBeenCalled();
    expect(termA2.dispose).not.toHaveBeenCalled();

    useTabStore.getState().openTab({
      id: `k8s-${a}`,
      type: "page",
      label: "k8s",
      meta: { type: "page", pageId: "k8s-cluster", assetId: a },
    });
    act(() => useTabStore.getState().closeTab(`k8s-${a}`));
    expect(StopK8sExec).toHaveBeenCalledWith("sess-a2");
    expect(termA2.dispose).toHaveBeenCalled();
    expect(StopK8sExec).not.toHaveBeenCalledWith("sess-b1");
  });

  it("stops a session that connects after its tab already closed", async () => {
    const id = cluster();
    let resolve!: (sessionId: string) => void;
    vi.mocked(StartK8sExec).mockReturnValueOnce(new Promise((r) => (resolve = r)) as never);
    const tabId = await openTerminal(id, pod("api-0"));
    act(() => useK8sStore.getState().closeTabs(id, [tabId]));
    expect(StopK8sExec).not.toHaveBeenCalled();
    await act(async () => resolve("sess-late"));
    expect(StopK8sExec).toHaveBeenCalledWith("sess-late");
    expect(handlers.has("k8s:exec:data:sess-late")).toBe(false);
  });

  it("keeps the output and shows the session ended when the container exits or the start fails", async () => {
    const id = cluster();
    const tabId = await openTerminal(id, pod("api-0"));
    await waitFor(() => expect(handlers.has("k8s:exec:end:sess-1")).toBe(true));
    const term = lastTerminal();
    act(() => handlers.get("k8s:exec:end:sess-1")!({ reason: "exited", exitCode: 137 }));
    expect(useK8sStore.getState().terminals[id][tabId]).toMatchObject({
      status: "ended",
      end: { reason: "exited", exitCode: 137 },
    });
    expect(term.write).toHaveBeenLastCalledWith(expect.stringContaining("137"));
    expect(term.dispose).not.toHaveBeenCalled();

    vi.mocked(StartK8sExec).mockRejectedValueOnce(new Error('container "nope" not found'));
    const failed = await openTerminal(id, pod("api-0"));
    await waitFor(() => expect(useK8sStore.getState().terminals[id][failed].status).toBe("ended"));
    expect(useK8sStore.getState().terminals[id][failed].end).toEqual({
      reason: "error",
      error: 'container "nope" not found',
    });
    expect(lastTerminal().write).toHaveBeenLastCalledWith(expect.stringContaining('container "nope" not found'));
  });
});
