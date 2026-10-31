import { describe, it, expect, beforeEach, vi } from "vitest";

// A backend tool call can be held open until the test releases it, so two calls
// of the same extension are in flight at the same time — the situation the
// invocation id exists for. Calls nobody holds resolve at once with "{}".
const pendingCalls = new Map<string, (result: string) => void>();
let holdCalls = false;

vi.mock("../../wailsjs/go/extension/Extension", () => ({
  CallExtensionTool: vi.fn((_ext: string, _tool: string, _args: string, invocationId: string, _assetId: number) =>
    holdCalls ? new Promise<string>((resolve) => pendingCalls.set(invocationId, resolve)) : Promise.resolve("{}")
  ),
  CancelExtensionTool: vi.fn(async () => undefined),
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

import { CallExtensionTool, CancelExtensionTool } from "../../wailsjs/go/extension/Extension";
import { createExtensionAPI } from "../extension/api";

function emit(payload: { extension: string; invocationId: string; eventType: string; data: unknown }) {
  for (const cb of listeners.get("ext:tool:event") ?? []) cb(payload);
}

function invocationIdOf(callIndex: number): string {
  return vi.mocked(CallExtensionTool).mock.calls[callIndex][3];
}

describe("extension page API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    pendingCalls.clear();
    listeners.clear();
    holdCalls = false;
  });

  it("routes a tool call's events to that call's onEvent, not to every call of the extension", async () => {
    holdCalls = true;
    const api = createExtensionAPI();
    const first: unknown[] = [];
    const second: unknown[] = [];

    const a = api.callTool("oss", "upload", { file: "a" }, 12, { onEvent: (e) => first.push(e.data) });
    const b = api.callTool("oss", "upload", { file: "b" }, 12, { onEvent: (e) => second.push(e.data) });
    const [idA, idB] = [invocationIdOf(0), invocationIdOf(1)];
    expect(idA).not.toEqual(idB);

    emit({ extension: "oss", invocationId: idA, eventType: "progress", data: { pct: 10 } });
    emit({ extension: "oss", invocationId: idB, eventType: "progress", data: { pct: 90 } });
    // Another extension's stream must not reach either.
    emit({ extension: "kafka", invocationId: idA, eventType: "progress", data: { pct: 50 } });

    expect(first).toEqual([{ pct: 10 }]);
    expect(second).toEqual([{ pct: 90 }]);

    pendingCalls.get(idA)?.("{}");
    pendingCalls.get(idB)?.("{}");
    await Promise.all([a, b]);
  });

  it("keeps delivering events to a call after another call finishes, and stops once its own does", async () => {
    holdCalls = true;
    const api = createExtensionAPI();
    const survivor: unknown[] = [];

    const doomed = api.callTool("oss", "upload", {}, 12, { onEvent: () => undefined });
    const alive = api.callTool("oss", "upload", {}, 12, { onEvent: (e) => survivor.push(e.data) });
    const [idDoomed, idAlive] = [invocationIdOf(0), invocationIdOf(1)];

    pendingCalls.get(idDoomed)?.("{}");
    await doomed;

    emit({ extension: "oss", invocationId: idAlive, eventType: "progress", data: { pct: 42 } });
    expect(survivor).toEqual([{ pct: 42 }]);

    pendingCalls.get(idAlive)?.("{}");
    await alive;
    emit({ extension: "oss", invocationId: idAlive, eventType: "progress", data: { pct: 99 } });
    expect(survivor).toEqual([{ pct: 42 }]);
    expect(listeners.get("ext:tool:event")?.size ?? 0).toBe(0);
  });

  it("cancels only the call whose signal was aborted", async () => {
    holdCalls = true;
    const api = createExtensionAPI();
    const controller = new AbortController();
    const a = api.callTool("oss", "upload", {}, 12, { signal: controller.signal, onEvent: () => undefined });
    const b = api.callTool("oss", "upload", {}, 12, { onEvent: () => undefined });
    const [idA, idB] = [invocationIdOf(0), invocationIdOf(1)];

    controller.abort();
    await expect(a).rejects.toMatchObject({ name: "AbortError" });

    expect(CancelExtensionTool).toHaveBeenCalledTimes(1);
    expect(CancelExtensionTool).toHaveBeenCalledWith(idA);

    pendingCalls.get(idB)?.('{"ok":true}');
    await expect(b).resolves.toEqual({ ok: true });
  });

  // A page opened on an asset names it; the guest reads it off the call envelope
  // rather than from an argument the page had to repeat.
  it("names the asset a call runs against, and says so when there is none", async () => {
    const api = createExtensionAPI();

    await api.callTool("notebook", "note_list", {}, 12);
    expect(CallExtensionTool).toHaveBeenCalledWith("notebook", "note_list", "{}", expect.any(String), 12);

    await api.callTool("notebook", "note_list", {});
    expect(CallExtensionTool).toHaveBeenLastCalledWith("notebook", "note_list", "{}", expect.any(String), 0);
  });

  // The invocation id is what CancelExtensionTool names and what every event of
  // the call carries: it must be a real, call-unique token, not an empty string
  // or a constant reused across calls (either of which would let one call's abort
  // stop another, or be refused by the backend outright).
  it("mints a distinct invocation id for each callTool call", async () => {
    const api = createExtensionAPI();

    await api.callTool("notebook", "note_put", { key: "a" }, 12);
    await api.callTool("notebook", "note_put", { key: "b" }, 12);

    const firstInvocationId = invocationIdOf(0);
    const secondInvocationId = invocationIdOf(1);
    expect(firstInvocationId).toEqual(expect.any(String));
    expect(firstInvocationId).not.toBe("");
    expect(firstInvocationId).not.toEqual(secondInvocationId);
  });

  // A page that stops waiting (navigates away, the user hits stop) aborts its
  // signal; the backend call is canceled by the id this call was made under, so
  // the guest — and any host IO it is blocked in — is interrupted instead of
  // holding one of the extension's few instance slots until it finishes.
  it("cancels the backend call when the caller aborts", async () => {
    let rejectCall: (err: Error) => void = () => undefined;
    vi.mocked(CallExtensionTool).mockImplementationOnce(
      () => new Promise<string>((_resolve, reject) => (rejectCall = reject))
    );
    vi.mocked(CancelExtensionTool).mockImplementationOnce(async () => rejectCall(new Error("context canceled")));
    const api = createExtensionAPI();
    const controller = new AbortController();

    const call = api.callTool("es", "search", { q: "*" }, 12, { signal: controller.signal });
    controller.abort();

    await expect(call).rejects.toMatchObject({ name: "AbortError" });
    expect(CancelExtensionTool).toHaveBeenCalledWith(invocationIdOf(0));
  });

  it("does not start a call whose signal is already aborted", async () => {
    const api = createExtensionAPI();
    const controller = new AbortController();
    controller.abort();

    await expect(api.callTool("es", "search", {}, 12, { signal: controller.signal })).rejects.toMatchObject({
      name: "AbortError",
    });
    expect(CallExtensionTool).not.toHaveBeenCalled();
  });

  it("returns the result of a call that finishes before its signal is aborted", async () => {
    const api = createExtensionAPI();
    const controller = new AbortController();

    await expect(api.callTool("es", "search", {}, 12, { signal: controller.signal })).resolves.toEqual({});
    controller.abort();
    expect(CancelExtensionTool).not.toHaveBeenCalled();
  });
});
