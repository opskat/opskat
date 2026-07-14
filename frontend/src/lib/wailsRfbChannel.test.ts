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

  it("reports WriteVNC failures through the channel error callback", async () => {
    captureHandlers();
    const failure = new Error("write failed");
    vi.mocked(WriteVNC).mockRejectedValue(failure);
    const channel = new WailsRfbChannel("sess-1");
    const onerror = vi.fn();
    channel.onerror = onerror;

    channel.send(new Uint8Array([1]));

    await vi.waitFor(() => expect(onerror).toHaveBeenCalledWith(failure));
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

  it("fires onclose when the closed event arrives", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const onclose = vi.fn();
    channel.onclose = onclose;
    handlers["vnc:closed:sess-1"]!();
    expect(onclose).toHaveBeenCalledTimes(1);
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
