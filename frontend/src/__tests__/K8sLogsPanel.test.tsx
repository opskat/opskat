import { useImperativeHandle, useState, type Ref } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  FetchK8sPodLogsTail,
  OpenK8sPodLogsBuffer,
  SaveK8sPodLogs,
  StartK8sPodLogs,
  StopK8sPodLogs,
} from "../../wailsjs/go/k8s/K8s";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { K8sLogsPanel } from "@/components/k8s/K8sLogsPanel";
import type { LogTabState, LogTabStateUpdate } from "@/components/k8s/k8sLogState";

const terminalSpies = vi.hoisted(() => ({
  clear: vi.fn(),
  write: vi.fn(),
  prepend: vi.fn(),
  getLogText: vi.fn(() => "cached log line\n"),
}));

const reachTopHandler = vi.hoisted(() => ({ current: undefined as undefined | (() => void) }));

vi.mock("@/components/k8s/K8sLogTerminal", () => ({
  K8sLogTerminal: function MockK8sLogTerminal({ ref, onReachTop }: { ref?: Ref<unknown>; onReachTop?: () => void }) {
    reachTopHandler.current = onReachTop;
    useImperativeHandle(ref, () => ({
      clear: terminalSpies.clear,
      write: terminalSpies.write,
      prepend: terminalSpies.prepend,
      getLogText: terminalSpies.getLogText,
    }));
    return <div data-testid="k8s-log-terminal" />;
  },
}));

function decodeTerminalWrite(data: string | Uint8Array) {
  if (typeof data === "string") return data;
  return new TextDecoder().decode(data);
}

function LogPanelWithHistory({ loadedTailLines = 2 }: { loadedTailLines?: number } = {}) {
  const [podName, setPodName] = useState("pod-a");
  const [state, setState] = useState<LogTabState>({
    logStreamID: null,
    logContainer: "main",
    logError: null,
    currentPod: "pod-a",
    logBuffers: {
      "pod-a::main": {
        container: "main",
        loadedTailLines,
        olderPrefix: "",
        historyExhausted: false,
        chunks: [],
      },
    },
  });

  const handleStateChange = (update: LogTabStateUpdate) => {
    setState((prev) => (typeof update === "function" ? update(prev) : { ...prev, ...update }));
  };

  return (
    <K8sLogsPanel
      assetId={7}
      namespace="default"
      podName={podName}
      containers={[{ name: "main" }]}
      pods={[{ name: "pod-a" }, { name: "pod-b" }]}
      state={state}
      onStateChange={handleStateChange}
      onSwitchPod={(nextPod) => {
        setPodName(nextPod);
        setState((prev) => ({ ...prev, currentPod: nextPod }));
      }}
    />
  );
}

function DeploymentLogPanelHarness() {
  const [podName, setPodName] = useState("pod-a");
  const [state, setState] = useState<LogTabState>({
    logStreamID: null,
    logContainer: "",
    logError: null,
    currentPod: "pod-a",
    logBuffers: {},
  });

  const handleStateChange = (update: LogTabStateUpdate) => {
    setState((prev) => (typeof update === "function" ? update(prev) : { ...prev, ...update }));
  };

  return (
    <K8sLogsPanel
      assetId={7}
      namespace="default"
      podName={podName}
      containers={[{ name: "main" }]}
      pods={[{ name: "pod-a" }, { name: "pod-b" }]}
      state={state}
      onStateChange={handleStateChange}
      onSwitchPod={(nextPod) => {
        setPodName(nextPod);
        setState((prev) => ({ ...prev, currentPod: nextPod }));
      }}
    />
  );
}

describe("K8sLogsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    terminalSpies.clear.mockReset();
    terminalSpies.write.mockReset();
  });

  it("restores cached logs when switching away from a pod and back", async () => {
    const user = userEvent.setup();
    const eventHandlers = new Map<string, (payload?: string) => void>();

    vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
    vi.mocked(StopK8sPodLogs).mockResolvedValue(undefined as never);
    vi.mocked(EventsOn).mockImplementation(((event: string, handler: (payload?: string) => void) => {
      eventHandlers.set(event, handler);
      return vi.fn();
    }) as never);

    render(<DeploymentLogPanelHarness />);

    await waitFor(() => {
      expect(StartK8sPodLogs).toHaveBeenCalledWith(7, "default", "pod-a", "main", 200);
    });

    const logChunk = btoa("hello from pod-a\n");
    eventHandlers.get("k8s:log:stream-1")?.(logChunk);

    await waitFor(() => {
      expect(terminalSpies.write).toHaveBeenCalledTimes(1);
    });
    expect(decodeTerminalWrite(terminalSpies.write.mock.calls[0]![0])).toBe("hello from pod-a\n");

    await user.selectOptions(screen.getByRole("combobox"), "pod-b");

    await waitFor(() => {
      expect(StopK8sPodLogs).toHaveBeenCalledWith("stream-1");
    });
    expect(terminalSpies.clear).toHaveBeenCalled();

    await user.selectOptions(screen.getByRole("combobox"), "pod-a");

    await waitFor(() => {
      expect(terminalSpies.write).toHaveBeenCalledTimes(2);
    });
    expect(decodeTerminalWrite(terminalSpies.write.mock.calls[1]![0])).toBe("hello from pod-a\n");
  });

  it("loads older logs when the terminal scrolls to the top", async () => {
    vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
    vi.mocked(StopK8sPodLogs).mockResolvedValue(undefined as never);
    vi.mocked(FetchK8sPodLogsTail).mockResolvedValue("line-1\nline-2\nline-3\nline-4\n" as never);

    render(<LogPanelWithHistory />);
    await waitFor(() => {
      expect(StartK8sPodLogs).toHaveBeenCalledWith(7, "default", "pod-a", "main", 200);
    });

    reachTopHandler.current?.();
    await waitFor(() => {
      expect(FetchK8sPodLogsTail).toHaveBeenCalledWith(7, "default", "pod-a", "main", 202);
      expect(terminalSpies.prepend).toHaveBeenCalledWith("line-1\nline-2\n");
    });
  });

  it("shows an animated loading indicator while older logs are being fetched", async () => {
    vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
    vi.mocked(StopK8sPodLogs).mockResolvedValue(undefined as never);
    let resolveTail: (snapshot: string) => void = () => {};
    vi.mocked(FetchK8sPodLogsTail).mockReturnValue(
      new Promise<string>((resolve) => {
        resolveTail = resolve;
      }) as never
    );

    render(<LogPanelWithHistory />);
    await waitFor(() => {
      expect(StartK8sPodLogs).toHaveBeenCalled();
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();

    await act(async () => {
      reachTopHandler.current?.();
    });

    const indicator = screen.getByRole("status");
    expect(indicator).toHaveTextContent("asset.k8sPodLogsLoadingOlder");
    expect(indicator.querySelector("svg")).toHaveClass("animate-spin");

    await act(async () => {
      resolveTail("line-1\nline-2\nline-3\nline-4\n");
    });

    await waitFor(() => {
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
    });
  });

  it("opens the terminal buffer with the default app", async () => {
    const user = userEvent.setup();
    vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
    vi.mocked(StopK8sPodLogs).mockResolvedValue(undefined as never);
    vi.mocked(OpenK8sPodLogsBuffer).mockResolvedValue(undefined as never);

    render(<DeploymentLogPanelHarness />);
    await waitFor(() => {
      expect(StartK8sPodLogs).toHaveBeenCalled();
    });

    await user.click(screen.getByRole("button", { name: "asset.k8sPodLogsOpenExternal" }));

    await waitFor(() => {
      expect(OpenK8sPodLogsBuffer).toHaveBeenCalledWith(
        "cached log line\n",
        "default",
        "pod-a",
        "main"
      );
    });
  });

  it("downloads the full cluster log file instead of the terminal buffer", async () => {
    const user = userEvent.setup();
    vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
    vi.mocked(StopK8sPodLogs).mockResolvedValue(undefined as never);
    vi.mocked(SaveK8sPodLogs).mockResolvedValue(true as never);

    render(<DeploymentLogPanelHarness />);
    await waitFor(() => {
      expect(StartK8sPodLogs).toHaveBeenCalled();
    });

    await user.click(screen.getByRole("button", { name: "asset.k8sPodLogsDownload" }));

    await waitFor(() => {
      expect(SaveK8sPodLogs).toHaveBeenCalledWith(7, "default", "pod-a", "main");
    });
  });
});
