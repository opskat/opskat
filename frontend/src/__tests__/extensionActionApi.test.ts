import { describe, it, expect, beforeEach, vi } from "vitest";

// One backend action call resolves when the test releases it, so two runs of the
// same extension can be in flight at the same time — which is the situation the
// invocation id exists for.
const pendingCalls = new Map<string, (result: string) => void>();

vi.mock("../../wailsjs/go/extension/Extension", () => ({
  CallExtensionAction: vi.fn(
    (_ext: string, _action: string, _args: string, invocationId: string) =>
      new Promise<string>((resolve) => pendingCalls.set(invocationId, resolve))
  ),
  CallExtensionTool: vi.fn(async () => "{}"),
  CancelExtensionAction: vi.fn(async () => undefined),
}));

type Listener = (payload: unknown) => void;
const listeners = new Map<string, Set<Listener>>();

vi.mock("../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn((name: string, cb: Listener) => {
    const set = listeners.get(name) ?? new Set<Listener>();
    set.add(cb);
    listeners.set(name, set);
    return () => set.delete(cb);
  }),
  EventsOff: vi.fn((name: string) => listeners.delete(name)),
}));

import { CallExtensionAction, CancelExtensionAction } from "../../wailsjs/go/extension/Extension";
import { createExtensionAPI } from "../extension/api";

function emit(payload: { extension: string; invocationId: string; eventType: string; data: unknown }) {
  for (const cb of listeners.get("ext:action:event") ?? []) cb(payload);
}

describe("extension action API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    pendingCalls.clear();
    listeners.clear();
  });

  it("routes events to the run that produced them, not to every run of the extension", async () => {
    const api = createExtensionAPI();
    const first: unknown[] = [];
    const second: unknown[] = [];

    const a = api.startAction("oss", "upload", { file: "a" }, (e) => first.push(e.data));
    const b = api.startAction("oss", "upload", { file: "b" }, (e) => second.push(e.data));
    expect(a.invocationId).not.toEqual(b.invocationId);

    emit({ extension: "oss", invocationId: a.invocationId, eventType: "progress", data: { pct: 10 } });
    emit({ extension: "oss", invocationId: b.invocationId, eventType: "progress", data: { pct: 90 } });
    // Another extension's stream must not reach either.
    emit({ extension: "kafka", invocationId: a.invocationId, eventType: "progress", data: { pct: 50 } });

    expect(first).toEqual([{ pct: 10 }]);
    expect(second).toEqual([{ pct: 90 }]);

    pendingCalls.get(a.invocationId)?.("{}");
    pendingCalls.get(b.invocationId)?.("{}");
    await Promise.all([a.result, b.result]);
  });

  it("keeps delivering events to a run after another run of the same extension finishes", async () => {
    const api = createExtensionAPI();
    const survivor: unknown[] = [];

    const doomed = api.startAction("oss", "upload", {}, () => undefined);
    const alive = api.startAction("oss", "upload", {}, (e) => survivor.push(e.data));

    pendingCalls.get(doomed.invocationId)?.("{}");
    await doomed.result;

    emit({ extension: "oss", invocationId: alive.invocationId, eventType: "progress", data: { pct: 42 } });
    expect(survivor).toEqual([{ pct: 42 }]);

    pendingCalls.get(alive.invocationId)?.("{}");
    await alive.result;
  });

  it("cancels only the run it was asked to cancel", async () => {
    const api = createExtensionAPI();
    const a = api.startAction("oss", "upload", {}, () => undefined);
    const b = api.startAction("oss", "upload", {}, () => undefined);

    await a.cancel();

    expect(CancelExtensionAction).toHaveBeenCalledTimes(1);
    expect(CancelExtensionAction).toHaveBeenCalledWith("oss", a.invocationId);

    pendingCalls.get(a.invocationId)?.("{}");
    pendingCalls.get(b.invocationId)?.("{}");
    await Promise.all([a.result, b.result]);
  });

  it("passes the invocation id the caller will cancel with to the backend", async () => {
    const api = createExtensionAPI();
    const run = api.startAction("oss", "upload", { file: "x" });

    expect(CallExtensionAction).toHaveBeenCalledWith("oss", "upload", JSON.stringify({ file: "x" }), run.invocationId);

    pendingCalls.get(run.invocationId)?.('{"ok":true}');
    await expect(run.result).resolves.toEqual({ ok: true });
  });
});
