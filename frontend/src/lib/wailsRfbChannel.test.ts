import { describe, it, expect, vi, beforeEach } from "vitest";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { WriteRemoteDesktop } from "../../wailsjs/go/remote_desktop/RemoteDesktop";
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
    vi.mocked(WriteRemoteDesktop)
      .mockReset()
      .mockResolvedValue(undefined as never);
  });

  it("delivers a data event to onmessage as an ArrayBuffer", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const received: ArrayBuffer[] = [];
    channel.onmessage = (e) => received.push(e.data);

    handlers["remote_desktop:data:sess-1"]!(btoa(String.fromCharCode(1, 2, 3)));

    expect(received).toHaveLength(1);
    expect(Array.from(new Uint8Array(received[0]))).toEqual([1, 2, 3]);
  });

  it("encodes send() bytes to base64 for WriteRemoteDesktop", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.send(new Uint8Array([104, 105])); // "hi"
    expect(WriteRemoteDesktop).toHaveBeenCalledWith("sess-1", btoa("hi"));
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
    handlers["remote_desktop:closed:sess-1"]!();
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(channel.readyState).toBe("closed");
  });

  it("close() unsubscribes both events and marks closed", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.close();
    expect(EventsOff).toHaveBeenCalledWith("remote_desktop:data:sess-1");
    expect(EventsOff).toHaveBeenCalledWith("remote_desktop:closed:sess-1");
    expect(channel.readyState).toBe("closed");
  });
});
