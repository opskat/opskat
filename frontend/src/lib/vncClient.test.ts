import { beforeEach, describe, expect, it, vi } from "vitest";
import type RFB from "@novnc/novnc";
import { startVNCClient, VNCClientError, type VNCNegotiatedSecurity } from "./vncClient";

const { FakeRFB } = vi.hoisted(() => {
  class FakeRFB extends EventTarget {
    static latest: FakeRFB | undefined;
    static options: { credentials?: Record<string, string>; securityPolicy?: number[][] } | undefined;

    scaleViewport = false;
    clipViewport = false;
    resizeSession = true;
    background = "";
    approveServer = vi.fn();
    sendCredentials = vi.fn();
    disconnect = vi.fn();
    clipboardPasteFrom = vi.fn();
    sendKey = vi.fn();
    sendCtrlAltDel = vi.fn();

    constructor(
      _target: HTMLElement,
      _source: unknown,
      options?: { credentials?: Record<string, string>; securityPolicy?: number[][] }
    ) {
      super();
      FakeRFB.latest = this;
      FakeRFB.options = options;
    }
  }
  return { FakeRFB };
});

vi.mock("@novnc/novnc", () => ({ default: FakeRFB }));

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makeOptions(overrides: Record<string, unknown> = {}) {
  const source = {
    binaryType: "arraybuffer",
    protocol: "",
    readyState: "connecting",
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
    send: vi.fn(),
    close: vi.fn(),
  };
  const checkServerKey = vi.fn().mockResolvedValue({
    state: "first_use",
    host: "vnc.example.com",
    port: 5901,
    newFingerprint: "SHA256:new",
  });
  const trustServerKey = vi.fn().mockResolvedValue(undefined);
  const requestServerTrust = vi.fn().mockResolvedValue(true);
  const openSource = vi.fn();
  const startTransport = vi.fn().mockResolvedValue(undefined);
  const closeTransport = vi.fn();
  return {
    target: document.createElement("div"),
    source,
    sessionId: "session-1",
    username: "vnc-user",
    password: "secret",
    securityPolicy: [[129, 133]],
    checkServerKey,
    trustServerKey,
    requestServerTrust,
    openSource,
    startTransport,
    closeTransport,
    ...overrides,
  };
}

beforeEach(() => {
  FakeRFB.latest = undefined;
  FakeRFB.options = undefined;
  vi.clearAllMocks();
});

describe("startVNCClient", () => {
  it("constructs from the exported package without credentials and applies policy", () => {
    const options = makeOptions();
    const handle = startVNCClient(options);

    expect(handle.rfb).toBe(FakeRFB.latest as unknown as RFB);
    expect(FakeRFB.options).toEqual({ securityPolicy: [[129, 133]] });
    expect(options.openSource).toHaveBeenCalledTimes(1);
    expect(options.startTransport).toHaveBeenCalledTimes(1);
  });

  it("persists first-use trust before approval and only supplies requested credentials afterwards", async () => {
    const order: string[] = [];
    const options = makeOptions({
      checkServerKey: vi.fn(async () => {
        order.push("check");
        return {
          state: "first_use",
          host: "vnc.example.com",
          port: 5901,
          newFingerprint: "SHA256:new",
        };
      }),
      trustServerKey: vi.fn(async () => {
        order.push("trust");
      }),
    });
    const handle = startVNCClient(options);
    FakeRFB.latest!.approveServer.mockImplementation(() => order.push("approve"));

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", { detail: { publickey: new Uint8Array([1, 2, 3, 4]) } })
    );
    FakeRFB.latest!.dispatchEvent(new CustomEvent("credentialsrequired", { detail: { types: ["password"] } }));

    expect(FakeRFB.latest!.sendCredentials).not.toHaveBeenCalled();
    await vi.waitFor(() => expect(FakeRFB.latest!.approveServer).toHaveBeenCalledTimes(1));
    expect(options.checkServerKey).toHaveBeenCalledWith("session-1", "AQIDBA==");
    expect(options.trustServerKey).toHaveBeenCalledWith("session-1", "AQIDBA==", false);
    expect(order).toEqual(["check", "trust", "approve"]);
    expect(FakeRFB.latest!.sendCredentials).toHaveBeenCalledWith({ username: "vnc-user", password: "secret" });
    handle.cleanup();
  });

  it("auto-approves an exact durable match without prompting or persisting", async () => {
    const options = makeOptions({
      checkServerKey: vi.fn().mockResolvedValue({
        state: "match",
        host: "vnc.example.com",
        port: 5901,
        newFingerprint: "SHA256:trusted",
      }),
    });
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", { detail: { publickey: new Uint8Array([1, 2, 3, 4]) } })
    );

    await vi.waitFor(() => expect(FakeRFB.latest!.approveServer).toHaveBeenCalledTimes(1));
    expect(options.requestServerTrust).not.toHaveBeenCalled();
    expect(options.trustServerKey).not.toHaveBeenCalled();
    handle.cleanup();
  });

  it("requires explicit changed-key replacement and persists it before approval", async () => {
    const options = makeOptions({
      checkServerKey: vi.fn().mockResolvedValue({
        state: "changed",
        host: "vnc.example.com",
        port: 5901,
        oldFingerprint: "SHA256:old",
        newFingerprint: "SHA256:new",
      }),
    });
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", { detail: { publickey: new Uint8Array([1, 2, 3, 4]) } })
    );

    await vi.waitFor(() => expect(FakeRFB.latest!.approveServer).toHaveBeenCalledTimes(1));
    expect(options.requestServerTrust).toHaveBeenCalledWith(expect.objectContaining({ state: "changed" }));
    expect(options.trustServerKey).toHaveBeenCalledWith("session-1", "AQIDBA==", true);
    handle.cleanup();
  });

  it.each([
    "policy-rejected",
    "unsupported-security-type",
    "authentication-failed",
    "integrity-failed",
    "transport-closed",
  ] as const)("rejects result with the public typed failure %s", async (code) => {
    const options = makeOptions();
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("connectionfailure", { detail: { code, message: "opaque", securityType: 130 } })
    );

    await expect(handle.result).rejects.toMatchObject({ code, securityType: 130 });
    expect(options.closeTransport).toHaveBeenCalledTimes(1);
  });

  it("reports an unsatisfied credential request without sending partial credentials", async () => {
    const options = makeOptions({ password: undefined });
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(new CustomEvent("credentialsrequired", { detail: { types: ["password"] } }));

    await expect(handle.result).rejects.toMatchObject({ code: "unsatisfied-credentials" });
    expect(FakeRFB.latest!.sendCredentials).not.toHaveBeenCalled();
  });

  it.each([
    [
      "check",
      { checkServerKey: vi.fn().mockRejectedValue(new Error("storage unavailable")) },
      "server-key-check-failed",
    ],
    ["persist", { trustServerKey: vi.fn().mockRejectedValue(new Error("write failed")) }, "server-key-trust-failed"],
    ["reject", { requestServerTrust: vi.fn().mockResolvedValue(false) }, "server-key-rejected"],
  ])("stops on server identity %s errors before approval", async (_case, overrides, code) => {
    const options = makeOptions(overrides);
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", { detail: { publickey: new Uint8Array([1, 2, 3, 4]) } })
    );

    await expect(handle.result).rejects.toMatchObject({ code });
    expect(FakeRFB.latest!.approveServer).not.toHaveBeenCalled();
    expect(FakeRFB.latest!.sendCredentials).not.toHaveBeenCalled();
  });

  it("does not leave result pending when the transport closes cleanly before connect", async () => {
    const options = makeOptions();
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(new CustomEvent("disconnect", { detail: { clean: true } }));

    await expect(handle.result).rejects.toMatchObject({ code: "transport-closed" });
  });

  it("resolves the negotiated result only after connect", async () => {
    const options = makeOptions();
    const handle = startVNCClient(options);
    const security: VNCNegotiatedSecurity = {
      type: 130,
      name: "RA2ne_256",
      authenticationEncrypted: true,
      sessionEncrypted: false,
      aesBits: 256,
    };
    const settled = vi.fn();
    void handle.result.then(settled);

    FakeRFB.latest!.dispatchEvent(new CustomEvent("negotiatedsecurity", { detail: security }));
    await Promise.resolve();
    expect(settled).not.toHaveBeenCalled();
    FakeRFB.latest!.dispatchEvent(new CustomEvent("connect"));

    await expect(handle.result).resolves.toEqual(security);
  });

  it("makes cleanup idempotent and ignores late trust completion", async () => {
    const approval = deferred<boolean>();
    const options = makeOptions({ requestServerTrust: vi.fn(() => approval.promise) });
    const handle = startVNCClient(options);

    FakeRFB.latest!.dispatchEvent(
      new CustomEvent("serververification", { detail: { publickey: new Uint8Array([1, 2, 3, 4]) } })
    );
    await vi.waitFor(() => expect(options.requestServerTrust).toHaveBeenCalledTimes(1));
    handle.cleanup();
    handle.cleanup();
    approval.resolve(true);
    await Promise.resolve();
    await Promise.resolve();

    expect(FakeRFB.latest!.disconnect).toHaveBeenCalledTimes(1);
    expect(options.source.close).toHaveBeenCalledTimes(1);
    expect(options.closeTransport).toHaveBeenCalledTimes(1);
    expect(options.trustServerKey).not.toHaveBeenCalled();
    await expect(handle.result).rejects.toBeInstanceOf(VNCClientError);
  });
});
