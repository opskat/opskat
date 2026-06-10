import { describe, it, expect, beforeEach, vi } from "vitest";
import { createZmodemController } from "../components/terminal/zmodem/zmodemSession";
import { useSFTPStore } from "../stores/sftpStore";
import { useTerminalStore } from "../stores/terminalStore";
import { bytesToBase64 } from "../lib/terminalEncode";
import {
  ZmodemBeginDownload,
  ZmodemAppendChunk,
  ZmodemFinishDownload,
  ZmodemOpenUploadFiles,
  ZmodemPickUploadFiles,
  ZmodemReadChunk,
  ZmodemFinishUpload,
} from "../../wailsjs/go/ssh/SSH";
import { SFTPCancelTransfer } from "../../wailsjs/go/ssh/SSH";
import { notifySuccess } from "../lib/notify";

// 用可控的假 Sentry 捕获其 options，便于在测试里驱动 on_detect / to_terminal。
const hoisted = vi.hoisted(() => ({ sentryOpts: null as unknown as Record<string, (...a: unknown[]) => void> }));
vi.mock("zmodem.js", () => {
  class FakeSentry {
    consume = vi.fn();
    constructor(opts: Record<string, (...a: unknown[]) => void>) {
      hoisted.sentryOpts = opts;
    }
  }
  return { default: { Sentry: FakeSentry } };
});

vi.mock("../lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));
vi.mock("../i18n", () => ({ default: { t: (k: string) => k } }));

const flush = () => new Promise((r) => setTimeout(r, 0));

function makeReceiveSession() {
  const handlers: Record<string, (arg?: unknown) => void> = {};
  return {
    type: "receive" as const,
    on: (ev: string, fn: (arg?: unknown) => void) => {
      handlers[ev] = fn;
    },
    start: vi.fn(),
    abort: vi.fn(),
    close: vi.fn().mockResolvedValue(undefined),
    send_offer: vi.fn(),
    fire: (ev: string, arg?: unknown) => handlers[ev]?.(arg),
  };
}

function makeOffer(details: { name: string; size: number }) {
  const inputHandlers: Array<(p: number[]) => void> = [];
  let resolveAccept!: () => void;
  return {
    get_details: () => details,
    on: (ev: string, fn: (p: number[]) => void) => {
      if (ev === "input") inputHandlers.push(fn);
    },
    accept: vi.fn().mockImplementation(() => new Promise<void>((res) => (resolveAccept = res))),
    skip: vi.fn().mockResolvedValue(undefined),
    pushInput: (p: number[]) => inputHandlers.forEach((f) => f(p)),
    finishAccept: () => resolveAccept(),
  };
}

function makeSendSession() {
  return {
    type: "send" as const,
    on: vi.fn(),
    start: vi.fn(),
    abort: vi.fn(),
    close: vi.fn().mockResolvedValue(undefined),
    send_offer: vi.fn(),
  };
}

function makeController() {
  return createZmodemController({
    sessionId: "s1",
    write: vi.fn().mockResolvedValue(undefined),
    toTerminal: vi.fn(),
  });
}

describe("zmodemSession controller", () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    hoisted.sentryOpts = null as unknown as Record<string, (...a: unknown[]) => void>;
    useSFTPStore.setState({ transfers: {}, fileManagerOpenTabs: {}, fileManagerPaths: {} });
    // resolveTarget 用 tabData 把 sessionId 反查 tabId。
    useTerminalStore.setState({ tabData: { tab1: { panes: { s1: { sessionId: "s1" } } } } } as never);
  });

  it("forwards bytes to the Sentry and routes Sentry's to_terminal to toTerminal", () => {
    const toTerminal = vi.fn();
    const ctrl = createZmodemController({ sessionId: "s1", write: vi.fn().mockResolvedValue(undefined), toTerminal });

    const bytes = new Uint8Array([1, 2, 3]);
    ctrl.consume(bytes);
    // consume 透传到假 Sentry。
    // (FakeSentry.consume 是实例方法 spy，无法直接取，但 to_terminal 路由可验)
    hoisted.sentryOpts.to_terminal([9, 9]);
    expect(toTerminal).toHaveBeenCalledWith(new Uint8Array([9, 9]));
  });

  it("on detect of a receive session: opens file manager and becomes active", () => {
    const ctrl = makeController();
    const session = makeReceiveSession();
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    expect(ctrl.isActive()).toBe(true);
    expect(useSFTPStore.getState().fileManagerOpenTabs["tab1"]).toBe(true);
    expect(session.start).toHaveBeenCalled();
  });

  it("download: Begin → input→AppendChunk → accept→Finish → notify", async () => {
    vi.mocked(ZmodemBeginDownload).mockResolvedValue("z-1");
    makeController();
    const session = makeReceiveSession();
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    const offer = makeOffer({ name: "a.bin", size: 6 });
    session.fire("offer", offer);
    await flush();

    expect(ZmodemBeginDownload).toHaveBeenCalledWith("s1", "a.bin", 6);
    // 进度订阅复用 sftpStore：store 里已建条目。
    expect(useSFTPStore.getState().transfers["z-1"]).toBeDefined();
    expect(useSFTPStore.getState().transfers["z-1"].direction).toBe("download");

    offer.pushInput([10, 20, 30]);
    expect(ZmodemAppendChunk).toHaveBeenCalledWith("z-1", bytesToBase64(new Uint8Array([10, 20, 30])));

    offer.finishAccept();
    await flush();
    expect(ZmodemFinishDownload).toHaveBeenCalledWith("z-1");
    expect(notifySuccess).toHaveBeenCalled();
  });

  it("download offer skipped when user cancels the save dialog", async () => {
    vi.mocked(ZmodemBeginDownload).mockResolvedValue(""); // 用户取消
    makeController();
    const session = makeReceiveSession();
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    const offer = makeOffer({ name: "a.bin", size: 6 });
    session.fire("offer", offer);
    await flush();

    expect(offer.skip).toHaveBeenCalled();
    expect(ZmodemFinishDownload).not.toHaveBeenCalled();
  });

  it("cancel routing: registered ZMODEM transfer aborts the session, not SFTPCancelTransfer", async () => {
    vi.mocked(ZmodemBeginDownload).mockResolvedValue("z-1");
    makeController();
    const session = makeReceiveSession();
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });
    const offer = makeOffer({ name: "a.bin", size: 6 });
    session.fire("offer", offer);
    await flush();

    useSFTPStore.getState().cancelTransfer("z-1");
    expect(session.abort).toHaveBeenCalled();
    expect(SFTPCancelTransfer).not.toHaveBeenCalled();

    // 未注册的 transferId 回落到 SFTP 默认取消。
    useSFTPStore.getState().cancelTransfer("sftp-x");
    expect(SFTPCancelTransfer).toHaveBeenCalledWith("sftp-x");
  });

  it("upload: pick → send_offer → ReadChunk loop → end → Finish", async () => {
    vi.mocked(ZmodemPickUploadFiles).mockResolvedValue([
      { transferId: "u-1", name: "f.txt", size: 3, mtime: 0 },
    ] as never);
    vi.mocked(ZmodemReadChunk)
      .mockResolvedValueOnce({ data: bytesToBase64(new Uint8Array([1, 2, 3])), eof: false } as never)
      .mockResolvedValueOnce({ data: "", eof: true } as never);

    const xfer = { send: vi.fn(), end: vi.fn().mockResolvedValue(undefined) };
    const session = makeSendSession();
    session.send_offer.mockResolvedValue(xfer);

    makeController();
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    await vi.waitFor(() => expect(ZmodemFinishUpload).toHaveBeenCalledWith("u-1"));
    expect(session.send_offer).toHaveBeenCalledWith({ name: "f.txt", size: 3, mtime: undefined });
    expect(xfer.send).toHaveBeenCalledWith(new Uint8Array([1, 2, 3]));
    expect(xfer.end).toHaveBeenCalled();
    expect(useSFTPStore.getState().transfers["u-1"]?.direction).toBe("upload");
  });

  it("upload: queued drag files use OpenUploadFiles without opening picker", async () => {
    vi.mocked(ZmodemOpenUploadFiles).mockResolvedValue([
      { transferId: "u-2", name: "drag.txt", size: 2, mtime: 0 },
    ] as never);
    vi.mocked(ZmodemReadChunk)
      .mockResolvedValueOnce({ data: bytesToBase64(new Uint8Array([4, 5])), eof: false } as never)
      .mockResolvedValueOnce({ data: "", eof: true } as never);

    const xfer = { send: vi.fn(), end: vi.fn().mockResolvedValue(undefined) };
    const session = makeSendSession();
    session.send_offer.mockResolvedValue(xfer);

    const controller = makeController();
    controller.queueUploadFiles(["C:/tmp/drag.txt"]);
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    await vi.waitFor(() => expect(ZmodemFinishUpload).toHaveBeenCalledWith("u-2"));
    expect(ZmodemOpenUploadFiles).toHaveBeenCalledWith("s1", ["C:/tmp/drag.txt"]);
    expect(ZmodemPickUploadFiles).not.toHaveBeenCalled();
    expect(session.send_offer).toHaveBeenCalledWith({ name: "drag.txt", size: 2, mtime: undefined });
    expect(xfer.send).toHaveBeenCalledWith(new Uint8Array([4, 5]));
    expect(xfer.end).toHaveBeenCalled();
    expect(useSFTPStore.getState().transfers["u-2"]?.direction).toBe("upload");
  });

  it("upload: expired drag queue falls back to the picker", async () => {
    vi.useFakeTimers();
    vi.mocked(ZmodemPickUploadFiles).mockResolvedValue([
      { transferId: "u-pick", name: "picked.txt", size: 0, mtime: 0 },
    ] as never);
    const session = makeSendSession();
    session.send_offer.mockResolvedValue(undefined);

    const controller = makeController();
    controller.queueUploadFiles(["C:/tmp/stale.txt"]);
    vi.advanceTimersByTime(10_001);
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    await vi.waitFor(() => expect(ZmodemPickUploadFiles).toHaveBeenCalledWith("s1"));
    expect(ZmodemOpenUploadFiles).not.toHaveBeenCalled();
  });

  it("upload cancel interrupts the remote rz process", async () => {
    vi.mocked(ZmodemOpenUploadFiles).mockResolvedValue([
      { transferId: "u-cancel", name: "drag.txt", size: 2, mtime: 0 },
    ] as never);

    const xfer = { send: vi.fn(), end: vi.fn().mockResolvedValue(undefined) };
    const session = makeSendSession();
    session.send_offer.mockResolvedValue(xfer);
    const write = vi.fn().mockResolvedValue(undefined);
    const controller = createZmodemController({ sessionId: "s1", write, toTerminal: vi.fn() });

    controller.queueUploadFiles(["C:/tmp/drag.txt"]);
    hoisted.sentryOpts.on_detect({ confirm: () => session, deny: vi.fn() });

    await vi.waitFor(() => expect(useSFTPStore.getState().transfers["u-cancel"]).toBeDefined());
    useSFTPStore.getState().cancelTransfer("u-cancel");

    expect(session.abort).toHaveBeenCalled();
    expect(write).toHaveBeenCalledWith("s1", bytesToBase64(new Uint8Array([3])));
  });
});
