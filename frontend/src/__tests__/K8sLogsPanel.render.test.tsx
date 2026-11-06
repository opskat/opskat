import { useEffect, useImperativeHandle, useState, type Ref } from "react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StartK8sPodLogs, StopK8sPodLogs } from "../../wailsjs/go/k8s/K8s";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { K8sLogsPanel } from "@/components/k8s/K8sLogsPanel";
import { MAX_LOG_CHUNKS, type LogTabState, type LogTabStateUpdate } from "@/components/k8s/k8sLogState";
import { profiled } from "./renderCount";

const terminalSpies = vi.hoisted(() => ({ clear: vi.fn(), write: vi.fn() }));

vi.mock("@/components/k8s/K8sLogTerminal", () => ({
  K8sLogTerminal: function MockK8sLogTerminal({ ref }: { ref?: Ref<unknown> }) {
    useImperativeHandle(ref, () => ({ clear: terminalSpies.clear, write: terminalSpies.write }));
    return <div data-testid="k8s-log-terminal" />;
  },
}));

const KEY = "pod-a::main::200";
let latestState: LogTabState;

// Stands in for K8sClusterPage: owns the log tab state, so a commit here is a page commit.
function PageHarness() {
  const [state, setState] = useState<LogTabState>({
    logStreamID: null,
    logContainer: "",
    logTailLines: 200,
    logError: null,
    currentPod: "pod-a",
    logBuffers: {},
  });
  useEffect(() => {
    latestState = state;
  });
  const onStateChange = (update: LogTabStateUpdate) =>
    setState((prev) => (typeof update === "function" ? update(prev) : { ...prev, ...update }));
  return (
    <K8sLogsPanel
      assetId={7}
      namespace="default"
      podName="pod-a"
      containers={[{ name: "main" }]}
      state={state}
      onStateChange={onStateChange}
    />
  );
}

describe("K8sLogsPanel render cost", () => {
  let frames: FrameRequestCallback[];
  const handlers = new Map<string, (payload?: string) => void>();

  beforeEach(() => {
    vi.clearAllMocks();
    terminalSpies.write.mockReset();
    handlers.clear();
    frames = [];
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => frames.push(cb));
    vi.stubGlobal("cancelAnimationFrame", (id: number) => {
      frames[id - 1] = () => {};
    });
    vi.mocked(StartK8sPodLogs).mockResolvedValue("stream-1" as never);
    vi.mocked(StopK8sPodLogs).mockResolvedValue(undefined as never);
    vi.mocked(EventsOn).mockImplementation(((event: string, handler: (payload?: string) => void) => {
      handlers.set(event, handler);
      return vi.fn();
    }) as never);
  });

  afterEach(() => vi.unstubAllGlobals());

  const runFrame = () => {
    const pending = frames;
    frames = [];
    act(() => pending.forEach((cb) => cb(0)));
  };
  const emit = (data: string) => handlers.get("k8s:log:stream-1")!(data);

  async function startStream() {
    const { tree, counter } = profiled(<PageHarness />);
    const view = render(tree);
    await userEvent.setup().click(screen.getByRole("button", { name: /asset\.k8sPodLogsStart/i }));
    await waitFor(() => expect(handlers.has("k8s:log:stream-1")).toBe(true));
    await act(async () => {});
    runFrame();
    return { counter, view };
  }

  it("N chunks in one frame commit the page at most once and still write every chunk to the terminal", async () => {
    const { counter } = await startStream();
    const before = counter.commits;
    const N = 50;
    // each event is its own task in production, so it is its own React batch
    for (let i = 0; i < N; i++) act(() => emit(btoa(`line ${i}\n`)));
    runFrame();
    const delta = counter.commits - before;
    console.info(`page commits for ${N} chunks in one frame: ${delta}`);
    expect(delta).toBeLessThanOrEqual(1);
    expect(terminalSpies.write).toHaveBeenCalledTimes(N);
    expect(latestState.logBuffers![KEY]!.chunks).toHaveLength(N);
  });

  it("keeps only the newest MAX_LOG_CHUNKS chunks", async () => {
    await startStream();
    for (let i = 0; i < MAX_LOG_CHUNKS + 5; i++) act(() => emit(btoa(`l${i}`)));
    runFrame();
    const chunks = latestState.logBuffers![KEY]!.chunks;
    expect(chunks).toHaveLength(MAX_LOG_CHUNKS);
    expect(chunks[0]).toBe(btoa("l5"));
  });

  it("flushes pending chunks into state on stream end without waiting for a frame", async () => {
    await startStream();
    act(() => emit(btoa("a")));
    act(() => handlers.get("k8s:logend:stream-1")!());
    expect(latestState.logBuffers![KEY]!.chunks).toEqual([btoa("a")]);
    expect(latestState.logStreamID).toBeNull();
  });

  it("flushes pending chunks to the parent on unmount while a frame is pending", async () => {
    const updates: LogTabStateUpdate[] = [];
    let state: LogTabState = {
      logStreamID: null,
      logContainer: "",
      logTailLines: 200,
      logError: null,
      currentPod: "pod-a",
      logBuffers: {},
    };
    const onStateChange = (u: LogTabStateUpdate) => {
      updates.push(u);
      state = typeof u === "function" ? u(state) : { ...state, ...u };
    };
    const view = render(
      <K8sLogsPanel
        assetId={7}
        namespace="default"
        podName="pod-a"
        containers={[{ name: "main" }]}
        state={state}
        onStateChange={onStateChange}
      />
    );
    await userEvent.setup().click(screen.getByRole("button", { name: /asset\.k8sPodLogsStart/i }));
    await waitFor(() => expect(handlers.has("k8s:log:stream-1")).toBe(true));
    act(() => emit(btoa("b")));
    view.unmount();
    expect(state.logBuffers![KEY]!.chunks).toEqual([btoa("b")]);
  });
});
