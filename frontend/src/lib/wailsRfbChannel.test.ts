import { describe, it, expect, vi, beforeEach } from "vitest";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { WriteVNC } from "../../wailsjs/go/vnc/VNC";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";

// 捕获 EventsOn 注册的处理器,供测试主动触发。
function captureHandlers() {
  const handlers: Record<string, (payload?: string) => void> = {};
  vi.mocked(EventsOn).mockImplementation(((event: string, h: (p?: string) => void) => {
    handlers[event] = h;
    return () => {};
  }) as never);
  return handlers;
}

function deferred() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("WailsRfbChannel", () => {
  beforeEach(() => {
    vi.mocked(EventsOn).mockReset();
    vi.mocked(EventsOff).mockReset();
    vi.mocked(WriteVNC)
      .mockReset()
      .mockResolvedValue(undefined as never);
  });

  it("delivers a data event to onmessage as an ArrayBuffer", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const received: ArrayBuffer[] = [];
    channel.onmessage = (e) => received.push(e.data);

    handlers["vnc:data:sess-1"]!(btoa(String.fromCharCode(1, 2, 3)));

    expect(received).toHaveLength(1);
    expect(Array.from(new Uint8Array(received[0]))).toEqual([1, 2, 3]);
  });

  it("encodes send() bytes to base64 for WriteVNC", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.send(new Uint8Array([104, 105])); // "hi"
    expect(WriteVNC).toHaveBeenCalledWith("sess-1", btoa("hi"));
  });

  it("delivers high-byte (>= 0x80) data events to onmessage without corruption", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const received: ArrayBuffer[] = [];
    channel.onmessage = (e) => received.push(e.data);

    handlers["vnc:data:sess-1"]!(btoa(String.fromCharCode(0x00, 0x7f, 0x80, 0xff)));

    expect(received).toHaveLength(1);
    expect(Array.from(new Uint8Array(received[0]))).toEqual([0x00, 0x7f, 0x80, 0xff]);
  });

  it("encodes send() high-byte (>= 0x80) bytes to base64 without corruption", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.send(new Uint8Array([0x80, 0xff]));
    expect(WriteVNC).toHaveBeenCalledWith("sess-1", btoa(String.fromCharCode(0x80, 0xff)));
  });

  it("serializes asynchronous IPC writes in send order", async () => {
    captureHandlers();
    const first = deferred();
    vi.mocked(WriteVNC)
      .mockImplementationOnce(() => first.promise as never)
      .mockResolvedValue(undefined as never);
    const channel = new WailsRfbChannel("sess-1");

    channel.send(new Uint8Array([1]));
    channel.send(new Uint8Array([2]));

    expect(WriteVNC).toHaveBeenCalledTimes(1);
    expect(WriteVNC).toHaveBeenLastCalledWith("sess-1", "AQ==");
    first.resolve();
    await vi.waitFor(() => expect(WriteVNC).toHaveBeenCalledTimes(2));
    expect(WriteVNC).toHaveBeenLastCalledWith("sess-1", "Ag==");
  });

  it("does not start queued writes after the channel closes", async () => {
    captureHandlers();
    const first = deferred();
    vi.mocked(WriteVNC).mockImplementationOnce(() => first.promise as never);
    const channel = new WailsRfbChannel("sess-1");

    channel.send(new Uint8Array([1]));
    channel.send(new Uint8Array([2]));
    channel.close();
    first.resolve();
    await first.promise;
    await Promise.resolve();

    expect(WriteVNC).toHaveBeenCalledTimes(1);
  });

  it("reports the first WriteVNC failure and drops later queued writes", async () => {
    captureHandlers();
    const first = deferred();
    vi.mocked(WriteVNC).mockImplementationOnce(() => first.promise as never);
    const channel = new WailsRfbChannel("sess-1");
    const onerror = vi.fn();
    channel.onerror = onerror;

    channel.send(new Uint8Array([1]));
    channel.send(new Uint8Array([2]));
    const failure = new Error("write failed");
    first.reject(failure);

    await vi.waitFor(() => expect(onerror).toHaveBeenCalledWith(failure));
    expect(onerror).toHaveBeenCalledTimes(1);
    expect(WriteVNC).toHaveBeenCalledTimes(1);
  });

  it("marks open exactly once and fires onopen", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const onopen = vi.fn();
    channel.onopen = onopen;
    channel.markOpen();
    channel.markOpen();
    expect(onopen).toHaveBeenCalledTimes(1);
    expect(channel.readyState).toBe("open");
  });

  it("fires onclose with an abnormal WebSocket close event when the transport closes", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const onclose = vi.fn();
    channel.onclose = onclose;
    handlers["vnc:closed:sess-1"]!();
    expect(onclose).toHaveBeenCalledWith({ code: 1006, reason: "VNC transport closed", wasClean: false });
    expect(EventsOff).toHaveBeenCalledWith("vnc:data:sess-1");
    expect(EventsOff).toHaveBeenCalledWith("vnc:closed:sess-1");
    expect(channel.readyState).toBe("closed");
  });

  it("close() unsubscribes both events and marks closed", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.close();
    expect(EventsOff).toHaveBeenCalledWith("vnc:data:sess-1");
    expect(EventsOff).toHaveBeenCalledWith("vnc:closed:sess-1");
    expect(channel.readyState).toBe("closed");
  });
});
