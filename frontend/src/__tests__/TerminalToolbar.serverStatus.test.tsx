import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TerminalToolbar } from "../components/terminal/TerminalToolbar";
import { useTerminalStore } from "../stores/terminalStore";
import { useSFTPStore } from "../stores/sftpStore";
import { GetSSHServerStatus } from "../../wailsjs/go/ssh/SSH";

describe("TerminalToolbar server status", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useSFTPStore.setState({
      fileManagerOpenTabs: {},
      fileManagerPaths: {},
      toggleFileManager: vi.fn(),
      transfers: {},
      fileManagerWidth: 420,
      setFileManagerWidth: vi.fn(),
      setFileManagerPath: vi.fn(),
    });
    useTerminalStore.setState({
      tabData: {
        "tab-1": {
          splitTree: { type: "terminal", sessionId: "ssh-1" },
          activePaneId: "ssh-1",
          panes: {
            "ssh-1": { sessionId: "ssh-1", transport: "ssh", connected: true, connectedAt: Date.now() },
          },
          directoryFollowMode: "off",
        },
      },
      sessionSync: {},
      connections: {},
      connectingAssetIds: new Set(),
    } as never);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("opens the server status dialog and loads snapshot data", async () => {
    vi.mocked(GetSSHServerStatus).mockResolvedValue({
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
    } as never);

    render(<TerminalToolbar tabId="tab-1" />);

    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));

    await waitFor(() => expect(GetSSHServerStatus).toHaveBeenCalledWith("ssh-1"));
    expect(screen.getByText("terminal.serverStatus.title")).toBeInTheDocument();
    expect(screen.getAllByText("prod-web-01").length).toBeGreaterThan(0);
    expect(screen.getByText("terminal.serverStatus.loadAverage")).toBeInTheDocument();
  });

  it("auto refreshes while the switch is enabled", async () => {
    const setIntervalSpy = vi.spyOn(window, "setInterval");
    vi.mocked(GetSSHServerStatus).mockResolvedValue({
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
    } as never);

    render(<TerminalToolbar tabId="tab-1" />);

    fireEvent.click(screen.getByRole("button", { name: "terminal.serverStatus.trigger" }));
    await waitFor(() => expect(GetSSHServerStatus).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole("switch"));
    expect(setIntervalSpy).toHaveBeenCalled();

    const refresh = setIntervalSpy.mock.calls.at(-1)?.[0];
    if (typeof refresh !== "function") {
      throw new Error("expected auto refresh callback");
    }
    await refresh();

    await waitFor(() => expect(GetSSHServerStatus).toHaveBeenCalledTimes(2));
  });

  it("renders backend errors in the dialog", async () => {
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
