import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useServerStatusStore } from "./serverStatusStore";
import { GetSSHServerStatus } from "../../wailsjs/go/ssh/SSH";

vi.mock("../../wailsjs/go/ssh/SSH", () => ({ GetSSHServerStatus: vi.fn() }));

const snap = (cpu: number) => ({
  hostname: "h",
  cpuPercent: cpu,
  memoryUsedBytes: 512,
  memoryTotalBytes: 1024,
  collectedAt: 1,
});

function reset() {
  const { sessions, deactivate } = useServerStatusStore.getState();
  Object.keys(sessions).forEach(deactivate);
}

describe("serverStatusStore", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(GetSSHServerStatus).mockReset();
    vi.mocked(GetSSHServerStatus).mockResolvedValue(snap(10) as never);
    reset();
  });
  afterEach(() => {
    reset();
    vi.useRealTimers();
  });

  it("activate is idempotent and samples immediately", async () => {
    useServerStatusStore.getState().activate("s1");
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    expect(GetSSHServerStatus).toHaveBeenCalledTimes(1);
    expect(useServerStatusStore.getState().sessions.s1.buffer).toHaveLength(1);
  });

  it("keeps sampling on the interval and caps the ring buffer at 120", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    for (let i = 0; i < 130; i++) {
      await vi.advanceTimersByTimeAsync(5000);
    }
    expect(useServerStatusStore.getState().sessions.s1.buffer.length).toBe(120);
  });

  it("setPaused stops and resumes sampling", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    useServerStatusStore.getState().setPaused("s1", true);
    const before = vi.mocked(GetSSHServerStatus).mock.calls.length;
    await vi.advanceTimersByTimeAsync(20000);
    expect(GetSSHServerStatus).toHaveBeenCalledTimes(before);
    useServerStatusStore.getState().setPaused("s1", false);
    await vi.advanceTimersByTimeAsync(0);
    expect(vi.mocked(GetSSHServerStatus).mock.calls.length).toBeGreaterThan(before);
  });

  it("deactivate clears timer and drops the session", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    useServerStatusStore.getState().deactivate("s1");
    expect(useServerStatusStore.getState().sessions.s1).toBeUndefined();
    const before = vi.mocked(GetSSHServerStatus).mock.calls.length;
    await vi.advanceTimersByTimeAsync(20000);
    expect(GetSSHServerStatus).toHaveBeenCalledTimes(before);
  });

  it("records error but keeps buffer + timer on a non-session failure", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0); // 1 good sample
    vi.mocked(GetSSHServerStatus).mockRejectedValueOnce(new Error("boom"));
    await vi.advanceTimersByTimeAsync(5000);
    const s = useServerStatusStore.getState().sessions.s1;
    expect(s).toBeDefined();
    expect(s.error).toContain("boom");
    expect(s.buffer).toHaveLength(1);
  });

  it("self-deactivates when the session is gone", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    vi.mocked(GetSSHServerStatus).mockRejectedValue(new Error("会话不存在: s1"));
    await vi.advanceTimersByTimeAsync(5000);
    expect(useServerStatusStore.getState().sessions.s1).toBeUndefined();
  });

  it("refreshNow fetches one sample even while paused", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    useServerStatusStore.getState().setPaused("s1", true);
    const before = vi.mocked(GetSSHServerStatus).mock.calls.length;
    await useServerStatusStore.getState().refreshNow("s1");
    expect(vi.mocked(GetSSHServerStatus).mock.calls.length).toBe(before + 1);
    expect(useServerStatusStore.getState().sessions.s1.buffer.length).toBeGreaterThan(1);
  });

  it("does not start a second request while one is already in flight", async () => {
    // First request hangs unresolved so it stays "in flight"; later calls use the default resolved mock.
    let resolveFirst!: (value: unknown) => void;
    vi.mocked(GetSSHServerStatus).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFirst = resolve;
      }) as never
    );

    useServerStatusStore.getState().activate("s1"); // tick #1 fires and suspends on the pending request
    expect(GetSSHServerStatus).toHaveBeenCalledTimes(1);

    // A rapid manual refresh (or interval tick) while #1 is in flight must be ignored, not run concurrently.
    void useServerStatusStore.getState().refreshNow("s1");
    await vi.advanceTimersByTimeAsync(5000);
    expect(GetSSHServerStatus).toHaveBeenCalledTimes(1);

    resolveFirst(snap(10));
    await vi.advanceTimersByTimeAsync(0);
    expect(useServerStatusStore.getState().sessions.s1.loading).toBe(false);
  });

  it("setSessionInterval restarts sampling at the new cadence", async () => {
    useServerStatusStore.getState().activate("s1");
    await vi.advanceTimersByTimeAsync(0);
    useServerStatusStore.getState().setSessionInterval("s1", 3000);
    const before = vi.mocked(GetSSHServerStatus).mock.calls.length;
    await vi.advanceTimersByTimeAsync(3000);
    expect(vi.mocked(GetSSHServerStatus).mock.calls.length).toBe(before + 1);
  });
});
