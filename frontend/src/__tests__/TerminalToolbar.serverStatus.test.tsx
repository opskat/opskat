import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TerminalToolbar } from "../components/terminal/TerminalToolbar";
import { useTerminalStore } from "../stores/terminalStore";
import { useSFTPStore } from "../stores/sftpStore";
import { useServerStatusStore } from "../stores/serverStatusStore";
import { GetSSHServerStatus } from "../../wailsjs/go/ssh/SSH";

// The SSH module is already fully mocked via setup.ts (mockBinderModule).
// We only need to configure the return value of GetSSHServerStatus here.

const snapshot = {
  hostname: "prod-web-01",
  os: "Linux",
  uptime: "up 12 days",
  cpuPercent: 24.5,
  load1: 0.41,
  load5: 0.38,
  load15: 0.35,
  memoryUsedBytes: 4294967296,
  memoryTotalBytes: 8589934592,
  diskMount: "/",
  diskUsedBytes: 6442450944,
  diskTotalBytes: 21474836480,
  collectedAt: Date.now(),
};

const nvidiaGPU = {
  index: 0,
  vendor: "NVIDIA",
  name: "NVIDIA RTX 4090",
  utilizationPercent: 94,
  memoryUsedBytes: 23050 * 1024 * 1024,
  memoryTotalBytes: 24564 * 1024 * 1024,
  temperatureC: 67,
  powerDrawWatts: 387,
  powerLimitWatts: 450,
  fanPercent: 58,
  computeProcessCount: 3,
};

function seedStores() {
  useSFTPStore.setState({
    fileManagerOpenTabs: {},
    fileManagerPaths: {},
    toggleFileManager: vi.fn(),
    transfers: {},
    fileManagerWidth: 420,
    setFileManagerWidth: vi.fn(),
    setFileManagerPath: vi.fn(),
  } as never);
  useTerminalStore.setState({
    tabData: {
      "tab-1": {
        splitTree: { type: "terminal", sessionId: "ssh-1" },
        activePaneId: "ssh-1",
        panes: { "ssh-1": { sessionId: "ssh-1", transport: "ssh", connected: true, connectedAt: Date.now() } },
        directoryFollowMode: "off",
      },
    },
    sessionSync: {},
    connections: {},
    connectingAssetIds: new Set(),
  } as never);
}

function resetServerStatus() {
  const { sessions, deactivate } = useServerStatusStore.getState();
  Object.keys(sessions).forEach(deactivate);
}

describe("TerminalToolbar server status", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetSSHServerStatus).mockResolvedValue(snapshot as never);
    seedStores();
    resetServerStatus();
  });
  afterEach(() => {
    resetServerStatus();
    vi.useRealTimers();
  });

  it("opens the dialog, lazily activates collection and renders the snapshot", async () => {
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    await waitFor(() => expect(GetSSHServerStatus).toHaveBeenCalledWith("ssh-1"));
    expect(screen.getByText("terminal.serverStatus.title")).toBeInTheDocument();
    expect(screen.getAllByText("prod-web-01").length).toBeGreaterThan(0);
    expect(screen.getByText("terminal.serverStatus.loadAverage")).toBeInTheDocument();
  });

  it("does not render a GPU section when the snapshot has no GPUs", async () => {
    vi.mocked(GetSSHServerStatus).mockResolvedValue({ ...snapshot, gpus: [] } as never);
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    await waitFor(() => expect(GetSSHServerStatus).toHaveBeenCalledWith("ssh-1"));
    expect(screen.queryByText("terminal.serverStatus.gpuAccelerators")).not.toBeInTheDocument();
  });

  it("renders one NVIDIA GPU with utilization, VRAM, metadata, and secondary metrics", async () => {
    vi.mocked(GetSSHServerStatus).mockResolvedValue({
      ...snapshot,
      gpuDriverVersion: "550.54",
      cudaVersion: "12.4",
      gpus: [nvidiaGPU],
    } as never);
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    expect(await screen.findByText("terminal.serverStatus.gpuAccelerators")).toBeInTheDocument();
    expect(screen.getByText("GPU 0 · NVIDIA RTX 4090")).toBeInTheDocument();
    expect(screen.getByText("94.0%")).toHaveClass("text-primary");
    expect(screen.getByText("94.0%")).not.toHaveClass("text-warning", "text-destructive");
    expect(screen.getByText("22.5 GB / 24.0 GB")).toHaveClass("text-info");
    expect(screen.getByText("550.54")).toBeInTheDocument();
    expect(screen.getByText("12.4")).toBeInTheDocument();
    expect(screen.getByText("67°C")).toBeInTheDocument();
    expect(screen.getByText("387 W / 450 W")).toBeInTheDocument();
    expect(screen.getByText("58.0%")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("renders unavailable individual GPU metrics as dashes", async () => {
    vi.mocked(GetSSHServerStatus).mockResolvedValue({
      ...snapshot,
      gpus: [{ index: 0, vendor: "NVIDIA", name: "NVIDIA Tesla T4" }],
    } as never);
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    const title = await screen.findByText("GPU 0 · NVIDIA Tesla T4");
    const card = title.closest("article");
    expect(card).not.toBeNull();
    expect(within(card as HTMLElement).getAllByText("-").length).toBeGreaterThanOrEqual(4);
    expect(within(card as HTMLElement).getAllByText("- / -")).toHaveLength(2);
  });

  it("renders multiple GPUs in a capped internal scroll region when more than two are present", async () => {
    vi.mocked(GetSSHServerStatus).mockResolvedValue({
      ...snapshot,
      gpus: [
        nvidiaGPU,
        { ...nvidiaGPU, index: 1, name: "NVIDIA A100" },
        { ...nvidiaGPU, index: 2, name: "NVIDIA L40S" },
      ],
    } as never);
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    expect(await screen.findByText("GPU 0 · NVIDIA RTX 4090")).toBeInTheDocument();
    expect(screen.getByText("GPU 1 · NVIDIA A100")).toBeInTheDocument();
    expect(screen.getByText("GPU 2 · NVIDIA L40S")).toBeInTheDocument();
    expect(screen.getByTestId("server-status-gpu-list")).toHaveClass("max-h-[360px]", "overflow-y-auto");
  });

  it("renders mixed vendors with stable device keys and per-device metadata", async () => {
    vi.mocked(GetSSHServerStatus).mockResolvedValue({
      ...snapshot,
      gpus: [
        {
          ...nvidiaGPU,
          id: "GPU-aaaa",
          pciBusId: "0000:01:00.0",
          driverVersion: "550.54",
          runtime: "CUDA",
          runtimeVersion: "12.4",
        },
        {
          index: 0,
          id: "amd-uuid",
          pciBusId: "0000:41:00.0",
          vendor: "AMD",
          name: "AMD Instinct MI250X",
          driverVersion: "6.8.5",
          runtime: "ROCm",
          runtimeVersion: "6.4.1",
        },
        {
          index: 0,
          id: "intel-uuid",
          pciBusId: "0000:bd:00.0",
          vendor: "Intel",
          name: "Intel Data Center GPU Max 1550",
          driverVersion: "1.3.30872",
        },
      ],
    } as never);
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    const nvidia = await screen.findByText("GPU 0 · NVIDIA RTX 4090");
    const amd = screen.getByText("GPU 0 · AMD Instinct MI250X");
    const intel = screen.getByText("GPU 0 · Intel Data Center GPU Max 1550");
    expect(nvidia.closest("article")).toHaveAttribute("data-gpu-key", "NVIDIA:id:GPU-aaaa");
    expect(amd.closest("article")).toHaveAttribute("data-gpu-key", "AMD:id:amd-uuid");
    expect(intel.closest("article")).toHaveAttribute("data-gpu-key", "Intel:id:intel-uuid");
    expect(screen.getByText("ROCm 6.4.1")).toBeInTheDocument();
    expect(screen.getByText("1.3.30872")).toBeInTheDocument();
    expect(screen.getByText("PCI 0000:bd:00.0")).toBeInTheDocument();
  });

  it("toggling auto-refresh off pauses the session collector", async () => {
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));
    await waitFor(() => expect(useServerStatusStore.getState().sessions["ssh-1"]).toBeDefined());

    fireEvent.click(screen.getByRole("switch"));
    await waitFor(() => expect(useServerStatusStore.getState().sessions["ssh-1"].paused).toBe(true));
  });

  it("renders backend errors while keeping the dialog open", async () => {
    vi.mocked(GetSSHServerStatus).mockRejectedValue(new Error("backend exploded"));
    render(<TerminalToolbar tabId="tab-1" />);
    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    expect(await screen.findByText(/terminal\.serverStatus\.error/)).toBeInTheDocument();
    expect(screen.getByText(/backend exploded/)).toBeInTheDocument();
  });

  it("does not render the server status button for non-ssh panes", () => {
    useTerminalStore.setState({
      tabData: {
        "tab-1": {
          splitTree: { type: "terminal", sessionId: "serial-1" },
          activePaneId: "serial-1",
          panes: {
            "serial-1": { sessionId: "serial-1", transport: "serial", connected: true, connectedAt: Date.now() },
          },
          directoryFollowMode: "off",
        },
      },
    } as never);
    render(<TerminalToolbar tabId="tab-1" />);
    expect(screen.queryByRole("button", { name: "terminal.serverStatus.trigger" })).toBeNull();
  });
});
