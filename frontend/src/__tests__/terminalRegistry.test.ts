import { describe, it, expect, vi, beforeEach } from "vitest";

const hoisted = vi.hoisted(() => {
  const eventHandlers = new Map<string, (...args: unknown[]) => void>();
  const writeSpy = vi.fn();
  const pasteSpy = vi.fn();
  const clipboardGetTextSpy = vi.fn();
  const disposeSpy = vi.fn();
  const reconnectBySessionMock = vi.fn();
  const terminalCtor = vi.fn();
  const bridgeDisposeSpy = vi.fn();
  const webglAddonCtor = vi.fn();
  const webglAddonDisposeSpy = vi.fn();
  const webglContextLossDisposeSpy = vi.fn();
  const webglClearTextureAtlasSpy = vi.fn();
  const webLinksAddonCtor = vi.fn();
  const webLinksAddonDisposeSpy = vi.fn();
  const setWebglEnabledSpy = vi.fn();
  const reportWebglFailureSpy = vi.fn();
  const browserOpenURLSpy = vi.fn();
  const linkProviderDisposeSpy = vi.fn();
  const attachUrlHighlighterSpy = vi.fn();
  const urlHighlighterDisposeSpy = vi.fn();
  const queueUploadFilesSpy = vi.fn();
  const zmodemAbortSpy = vi.fn();
  const zmodemDisposeSpy = vi.fn();
  const toastWarningSpy = vi.fn();
  const toastErrorSpy = vi.fn();
  const disposeOrder: string[] = [];
  const state: {
    capturedOnKey: ((e: { key: string }) => void) | null;
    linkProvider: {
      provideLinks: (bufferLineNumber: number, callback: (links: unknown[] | undefined) => void) => void;
    } | null;
    lines: Map<number, string>;
    textarea: HTMLTextAreaElement | null;
  } = {
    capturedOnKey: null,
    linkProvider: null,
    lines: new Map(),
    textarea: null,
  };
  return {
    eventHandlers,
    writeSpy,
    pasteSpy,
    clipboardGetTextSpy,
    disposeSpy,
    reconnectBySessionMock,
    terminalCtor,
    bridgeDisposeSpy,
    webglAddonCtor,
    webglAddonDisposeSpy,
    webglContextLossDisposeSpy,
    webglClearTextureAtlasSpy,
    webLinksAddonCtor,
    webLinksAddonDisposeSpy,
    setWebglEnabledSpy,
    reportWebglFailureSpy,
    browserOpenURLSpy,
    linkProviderDisposeSpy,
    attachUrlHighlighterSpy,
    urlHighlighterDisposeSpy,
    queueUploadFilesSpy,
    zmodemAbortSpy,
    zmodemDisposeSpy,
    toastWarningSpy,
    toastErrorSpy,
    disposeOrder,
    zmodemActive: false,
    state,
  };
});

vi.mock("../../wailsjs/runtime/runtime", () => ({
  EventsOn: (event: string, handler: (...args: unknown[]) => void) => {
    hoisted.eventHandlers.set(event, handler);
  },
  EventsOff: (event: string) => {
    hoisted.eventHandlers.delete(event);
  },
  ClipboardGetText: hoisted.clipboardGetTextSpy,
  BrowserOpenURL: hoisted.browserOpenURLSpy,
}));

vi.mock("../../wailsjs/go/ssh/SSH", () => ({
  WriteSSH: vi.fn().mockResolvedValue(undefined),
  ResizeSSH: vi.fn().mockResolvedValue(undefined),
  ConnectSSHAsync: vi.fn().mockResolvedValue("conn-ssh"),
  DisconnectSSH: vi.fn(),
  SplitSSH: vi.fn().mockResolvedValue("split-ssh"),
}));

vi.mock("../../wailsjs/go/serial/Serial", () => ({
  WriteSerial: vi.fn().mockResolvedValue(undefined),
  ResizeSerialTerminal: vi.fn().mockResolvedValue(undefined),
  ConnectSerialAsync: vi.fn().mockResolvedValue("conn-serial"),
  DisconnectSerial: vi.fn(),
}));

vi.mock("../../wailsjs/go/local/Local", () => ({
  WriteLocal: vi.fn().mockResolvedValue(undefined),
  ResizeLocalTerminal: vi.fn().mockResolvedValue(undefined),
  ConnectLocalAsync: vi.fn().mockResolvedValue("conn-local"),
  DisconnectLocal: vi.fn(),
  SplitLocal: vi.fn().mockResolvedValue("split-local"),
}));

vi.mock("@xterm/xterm", () => {
  class MockTerminal {
    rows = 24;
    buffer = {
      active: {
        baseY: 0,
        cursorY: 0,
        viewportY: 0,
        length: 24,
        getLine: (lineNumber: number) => {
          const line = hoisted.state.lines.get(lineNumber);
          return line === undefined ? undefined : { translateToString: () => line };
        },
      },
    };
    loadAddon = vi.fn((addon: { activate?: (terminal: MockTerminal) => void }) => {
      addon.activate?.(this);
    });
    open = vi.fn();
    write = hoisted.writeSpy;
    paste = hoisted.pasteSpy;
    onData = vi.fn(() => ({ dispose: vi.fn() }));
    onKey = vi.fn((handler: (e: { key: string }) => void) => {
      hoisted.state.capturedOnKey = handler;
      return { dispose: vi.fn() };
    });
    onWriteParsed = vi.fn(() => ({ dispose: vi.fn() }));
    onRender = vi.fn(() => ({ dispose: vi.fn() }));
    attachCustomKeyEventHandler = vi.fn();
    textarea = document.createElement("textarea");
    registerLinkProvider = vi.fn((provider) => {
      hoisted.state.linkProvider = provider;
      return { dispose: hoisted.linkProviderDisposeSpy };
    });
    dispose = vi.fn(() => {
      hoisted.disposeOrder.push("term");
      hoisted.disposeSpy();
    });
    constructor(options?: unknown) {
      hoisted.state.textarea = this.textarea;
      hoisted.terminalCtor(options);
    }
  }
  return { Terminal: MockTerminal };
});

vi.mock("@/components/terminal/terminalInputBridge", () => ({
  createTerminalInputBridge: vi.fn(() => ({
    setShortcuts: vi.fn(),
    setOnCopy: vi.fn(),
    setOnPaste: vi.fn(),
    setOnSelectAll: vi.fn(),
    setOnFind: vi.fn(),
    dispose: vi.fn(() => {
      hoisted.disposeOrder.push("bridge");
      hoisted.bridgeDisposeSpy();
    }),
  })),
}));

vi.mock("@xterm/addon-fit", () => ({ FitAddon: class {} }));
vi.mock("@xterm/addon-search", () => ({ SearchAddon: class {} }));
vi.mock("@xterm/addon-web-links", () => {
  class MockWebLinksAddon {
    private linkProviderDispose: { dispose: () => void } | undefined;
    constructor(private readonly handler: (event: MouseEvent, uri: string) => void) {
      hoisted.webLinksAddonCtor();
    }
    activate = vi.fn((terminal: { registerLinkProvider: (provider: unknown) => { dispose: () => void } }) => {
      this.linkProviderDispose = terminal.registerLinkProvider({
        provideLinks: (bufferLineNumber: number, callback: (links: unknown[] | undefined) => void) => {
          const line = hoisted.state.lines.get(bufferLineNumber - 1);
          const match = line?.match(/https?:\/\/[^\s<>"'`]+/i);
          if (!match) {
            callback(undefined);
            return;
          }
          const rawUrl = match[0];
          const url = rawUrl.replace(/[),.;!?\]}]+$/, "");
          callback([
            {
              text: url,
              range: {
                start: { x: (match.index ?? 0) + 1, y: bufferLineNumber },
                end: { x: (match.index ?? 0) + url.length, y: bufferLineNumber },
              },
              activate: (event: MouseEvent | undefined, text: string) => this.handler(event as MouseEvent, text),
            },
          ]);
        },
      });
    });
    dispose = vi.fn(() => {
      this.linkProviderDispose?.dispose();
      hoisted.disposeOrder.push("webLinks");
      hoisted.webLinksAddonDisposeSpy();
    });
  }
  return { WebLinksAddon: MockWebLinksAddon };
});
vi.mock("@xterm/addon-webgl", () => {
  class MockWebglAddon {
    constructor() {
      hoisted.webglAddonCtor();
    }
    onContextLoss = vi.fn(() => ({
      dispose: vi.fn(() => {
        hoisted.webglContextLossDisposeSpy();
      }),
    }));
    clearTextureAtlas = vi.fn(() => {
      hoisted.webglClearTextureAtlasSpy();
    });
    dispose = vi.fn(() => {
      hoisted.disposeOrder.push("webgl");
      hoisted.webglAddonDisposeSpy();
    });
  }
  return { WebglAddon: MockWebglAddon };
});
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

vi.mock("@/components/terminal/terminalUrlHighlighter", () => ({
  attachTerminalUrlHighlighter: (...args: unknown[]) => {
    hoisted.attachUrlHighlighterSpy(...args);
    return {
      setEnabled: vi.fn(),
      setColor: vi.fn(),
      dispose: () => {
        hoisted.disposeOrder.push("urlHighlighter");
        hoisted.urlHighlighterDisposeSpy();
      },
    };
  },
}));

vi.mock("@/components/terminal/zmodem/zmodemSession", () => ({
  createZmodemController: vi.fn((opts: { toTerminal: (bytes: Uint8Array) => void }) => ({
    consume: (bytes: Uint8Array) => opts.toTerminal(bytes),
    isActive: () => hoisted.zmodemActive,
    abort: hoisted.zmodemAbortSpy,
    dispose: hoisted.zmodemDisposeSpy,
    queueUploadFiles: hoisted.queueUploadFilesSpy,
  })),
}));

vi.mock("sonner", () => ({
  toast: {
    warning: hoisted.toastWarningSpy,
    error: hoisted.toastErrorSpy,
  },
}));

vi.mock("@/stores/terminalStore", async (importActual) => {
  const actual = await importActual<typeof import("@/stores/terminalStore")>();
  return {
    // 复用真实的 TRANSPORTS 表与 transport 网关函数（纯函数，无副作用），
    // useTerminalStore 仍替换为最小桩，避免拉起整个 store 的副作用。
    TRANSPORTS: actual.TRANSPORTS,
    transportForAsset: actual.transportForAsset,
    inferTransportFromSessionId: actual.inferTransportFromSessionId,
    useTerminalStore: {
      getState: () => ({
        markClosed: vi.fn(),
        reconnectBySession: hoisted.reconnectBySessionMock,
      }),
    },
  };
});

vi.mock("@/stores/terminalThemeStore", () => ({
  useTerminalThemeStore: {
    getState: () => ({
      setWebglEnabled: hoisted.setWebglEnabledSpy,
      reportWebglFailure: hoisted.reportWebglFailureSpy,
    }),
  },
}));

vi.mock("@/data/terminalFonts", () => ({
  withTerminalFontFallback: (s: string) => s,
  withTerminalFontIsolation: (_id: string, s: string) => s,
}));
vi.mock("@/lib/terminalEncode", () => ({
  bytesToBase64: (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes)),
}));

vi.mock("@/i18n", () => ({
  default: { t: (key: string) => `<<${key}>>` },
}));

import {
  getOrCreateTerminal,
  disposeTerminal,
  pasteIntoTerminal,
  pasteFromClipboard,
  uploadFilesWithRz,
} from "@/components/terminal/terminalRegistry";
import { TRANSPORTS, transportForAsset, inferTransportFromSessionId } from "@/stores/terminalStore";
import { WriteSSH } from "../../wailsjs/go/ssh/SSH";

interface TestTerminalLink {
  text: string;
  range: { start: { x: number; y: number }; end: { x: number; y: number } };
  activate: (event: MouseEvent | undefined, text: string) => void;
}

describe("TRANSPORTS", () => {
  it("TRANSPORTS 覆盖 ssh/serial/local 且字段齐全", () => {
    for (const key of ["ssh", "serial", "local"] as const) {
      const t = TRANSPORTS[key];
      expect(t.eventPrefix).toBe(key);
      expect(typeof t.write).toBe("function");
      expect(typeof t.resize).toBe("function");
      expect(typeof t.connectAsync).toBe("function");
      expect(typeof t.disconnect).toBe("function");
      expect(typeof t.canSplit).toBe("boolean");
      // canSplit 与 split 必须一致:可分屏的 transport 必须提供 split 实现,反之不提供。
      expect(typeof t.split === "function").toBe(t.canSplit);
    }
    // ssh 复用连接、local 再起一个同 shell 的 PTY,二者均可分屏;serial 物理端口不可复用。
    expect(TRANSPORTS.ssh.canSplit).toBe(true);
    expect(TRANSPORTS.serial.canSplit).toBe(false);
    expect(TRANSPORTS.local.canSplit).toBe(true);
    // 只有 ssh 同步 cwd / 暴露 SFTP，serial/local 没有目录能力。
    expect(TRANSPORTS.ssh.hasDirectorySync).toBe(true);
    expect(TRANSPORTS.serial.hasDirectorySync).toBe(false);
    expect(TRANSPORTS.local.hasDirectorySync).toBe(false);
  });

  it("transportForAsset maps asset type → transport", () => {
    expect(transportForAsset("serial")).toBe("serial");
    expect(transportForAsset("local")).toBe("local");
    expect(transportForAsset("ssh")).toBe("ssh");
    expect(transportForAsset("k8s")).toBe("ssh"); // unknown → ssh default
  });

  it("inferTransportFromSessionId maps session id prefix → transport", () => {
    expect(inferTransportFromSessionId("serial-1")).toBe("serial");
    expect(inferTransportFromSessionId("local-2")).toBe("local");
    expect(inferTransportFromSessionId("abc-3")).toBe("ssh");
  });
});

describe("terminalRegistry", () => {
  beforeEach(() => {
    hoisted.eventHandlers.clear();
    hoisted.state.capturedOnKey = null;
    hoisted.state.linkProvider = null;
    hoisted.state.lines.clear();
    hoisted.writeSpy.mockClear();
    hoisted.pasteSpy.mockClear();
    hoisted.clipboardGetTextSpy.mockReset();
    hoisted.disposeSpy.mockClear();
    hoisted.reconnectBySessionMock.mockClear();
    hoisted.terminalCtor.mockClear();
    hoisted.bridgeDisposeSpy.mockClear();
    hoisted.webglAddonCtor.mockClear();
    hoisted.webglAddonDisposeSpy.mockClear();
    hoisted.webglContextLossDisposeSpy.mockClear();
    hoisted.webglClearTextureAtlasSpy.mockClear();
    hoisted.webLinksAddonCtor.mockClear();
    hoisted.webLinksAddonDisposeSpy.mockClear();
    hoisted.setWebglEnabledSpy.mockClear();
    hoisted.reportWebglFailureSpy.mockClear();
    hoisted.browserOpenURLSpy.mockClear();
    hoisted.linkProviderDisposeSpy.mockClear();
    hoisted.attachUrlHighlighterSpy.mockClear();
    hoisted.urlHighlighterDisposeSpy.mockClear();
    hoisted.queueUploadFilesSpy.mockClear();
    hoisted.zmodemAbortSpy.mockClear();
    hoisted.zmodemDisposeSpy.mockClear();
    hoisted.toastWarningSpy.mockClear();
    hoisted.toastErrorSpy.mockClear();
    hoisted.zmodemActive = false;
    hoisted.state.textarea = null;
    vi.mocked(WriteSSH).mockClear();
    hoisted.disposeOrder.length = 0;
  });

  it("queues dropped files and writes rz to an SSH terminal", async () => {
    getOrCreateTerminal("sess-rz", { fontSize: 14, fontFamily: "mono", scrollback: 1000, transport: "ssh" });

    await expect(uploadFilesWithRz("sess-rz", ["C:/tmp/a.txt"])).resolves.toBe(true);

    expect(hoisted.queueUploadFilesSpy).toHaveBeenCalledWith(["C:/tmp/a.txt"]);
    expect(WriteSSH).toHaveBeenCalledWith("sess-rz", btoa("rz\r"));
    disposeTerminal("sess-rz");
  });

  it("does not start a second rz upload before the first one is detected", async () => {
    getOrCreateTerminal("sess-rz-pending", { fontSize: 14, fontFamily: "mono", scrollback: 1000, transport: "ssh" });

    await expect(uploadFilesWithRz("sess-rz-pending", ["C:/tmp/a.txt"])).resolves.toBe(true);
    await expect(uploadFilesWithRz("sess-rz-pending", ["C:/tmp/b.txt"])).resolves.toBe(false);

    expect(hoisted.toastWarningSpy).toHaveBeenCalledWith("<<zmodem.dragBusy>>");
    expect(hoisted.queueUploadFilesSpy).toHaveBeenCalledTimes(1);
    expect(WriteSSH).toHaveBeenCalledTimes(1);
    disposeTerminal("sess-rz-pending");
  });

  it("does not start rz upload for non-SSH terminals", async () => {
    getOrCreateTerminal("local-rz", { fontSize: 14, fontFamily: "mono", scrollback: 1000, transport: "local" });

    await expect(uploadFilesWithRz("local-rz", ["C:/tmp/a.txt"])).resolves.toBe(false);

    expect(hoisted.queueUploadFilesSpy).not.toHaveBeenCalled();
    expect(WriteSSH).not.toHaveBeenCalled();
    disposeTerminal("local-rz");
  });

  it("does not start rz upload while ZMODEM is active", async () => {
    hoisted.zmodemActive = true;
    getOrCreateTerminal("sess-rz-busy", { fontSize: 14, fontFamily: "mono", scrollback: 1000, transport: "ssh" });

    await expect(uploadFilesWithRz("sess-rz-busy", ["C:/tmp/a.txt"])).resolves.toBe(false);

    expect(hoisted.toastWarningSpy).toHaveBeenCalledWith("<<zmodem.dragBusy>>");
    expect(hoisted.queueUploadFilesSpy).not.toHaveBeenCalled();
    expect(WriteSSH).not.toHaveBeenCalled();
    disposeTerminal("sess-rz-busy");
  });

  it("writes the i18n closed hint and marks closed when ssh:closed fires", () => {
    getOrCreateTerminal("sess-1", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    const handler = hoisted.eventHandlers.get("ssh:closed:sess-1");
    expect(handler).toBeDefined();
    handler?.();
    const written = hoisted.writeSpy.mock.calls.map((c) => c[0]).join("");
    expect(written).toContain("<<ssh.session.closedHint>>");
    disposeTerminal("sess-1");
  });

  it("triggers reconnectBySession on Enter after close, and re-arms on the next close", () => {
    getOrCreateTerminal("sess-2", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    hoisted.eventHandlers.get("ssh:closed:sess-2")?.();
    expect(hoisted.state.capturedOnKey).toBeTruthy();

    hoisted.state.capturedOnKey?.({ key: "\r" });
    expect(hoisted.reconnectBySessionMock).toHaveBeenCalledWith("sess-2");
    expect(hoisted.reconnectBySessionMock).toHaveBeenCalledTimes(1);

    // 第二次 Enter 在同一次 closed 内不应再触发
    hoisted.state.capturedOnKey?.({ key: "\r" });
    expect(hoisted.reconnectBySessionMock).toHaveBeenCalledTimes(1);

    // 重新 closed 后,Enter 应当再次触发
    hoisted.eventHandlers.get("ssh:closed:sess-2")?.();
    hoisted.state.capturedOnKey?.({ key: "\r" });
    expect(hoisted.reconnectBySessionMock).toHaveBeenCalledTimes(2);

    disposeTerminal("sess-2");
  });

  it("ignores non-Enter keys after close", () => {
    getOrCreateTerminal("sess-3", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    hoisted.eventHandlers.get("ssh:closed:sess-3")?.();
    hoisted.state.capturedOnKey?.({ key: "a" });
    hoisted.state.capturedOnKey?.({ key: "\n" });
    expect(hoisted.reconnectBySessionMock).not.toHaveBeenCalled();
    disposeTerminal("sess-3");
  });

  it("does not trigger reconnect when not closed", () => {
    getOrCreateTerminal("sess-4", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    hoisted.state.capturedOnKey?.({ key: "\r" });
    expect(hoisted.reconnectBySessionMock).not.toHaveBeenCalled();
    disposeTerminal("sess-4");
  });

  it("loads the official web links addon and opens HTTP URLs through Wails", () => {
    hoisted.state.lines.set(0, "Docs: https://help.ubuntu.com, ip 10.2.4.16 load 0.06");
    getOrCreateTerminal("sess-url", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });

    expect(hoisted.webLinksAddonCtor).toHaveBeenCalledTimes(1);

    let links: TestTerminalLink[] | undefined;
    hoisted.state.linkProvider?.provideLinks(1, (provided) => {
      links = provided as TestTerminalLink[] | undefined;
    });

    expect(links).toHaveLength(1);
    expect(links?.[0].text).toBe("https://help.ubuntu.com");
    expect(links?.[0].range).toEqual({ start: { x: 7, y: 1 }, end: { x: 29, y: 1 } });

    const link = links?.[0];
    expect(link).toBeDefined();
    link?.activate(undefined, link.text);
    expect(hoisted.browserOpenURLSpy).toHaveBeenCalledWith("https://help.ubuntu.com");
    disposeTerminal("sess-url");
  });

  it("does not open terminal links on right click", () => {
    hoisted.state.lines.set(0, "Docs: https://help.ubuntu.com");
    getOrCreateTerminal("sess-url-right-click", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });

    let links: TestTerminalLink[] | undefined;
    hoisted.state.linkProvider?.provideLinks(1, (provided) => {
      links = provided as TestTerminalLink[] | undefined;
    });

    const rightClick = new MouseEvent("mouseup", { button: 2 });
    links?.[0].activate(rightClick, links[0].text);
    expect(hoisted.browserOpenURLSpy).not.toHaveBeenCalled();
    disposeTerminal("sess-url-right-click");
  });

  it("does not create links for bare IP addresses or numbers", () => {
    hoisted.state.lines.set(0, "IPv4 address: 10.2.4.16 load 0.06");
    getOrCreateTerminal("sess-no-url", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });

    let links: TestTerminalLink[] | undefined;
    hoisted.state.linkProvider?.provideLinks(1, (provided) => {
      links = provided as TestTerminalLink[] | undefined;
    });

    expect(links).toBeUndefined();
    disposeTerminal("sess-no-url");
  });

  it("disposes URL link subscriptions", () => {
    hoisted.state.lines.set(0, "Docs: https://help.ubuntu.com");
    getOrCreateTerminal("sess-url-dispose", {
      fontSize: 14,
      fontFamily: "mono",
      scrollback: 1000,
      theme: { brightBlue: "#89b4fa" },
    });

    disposeTerminal("sess-url-dispose");

    expect(hoisted.linkProviderDisposeSpy).toHaveBeenCalled();
    expect(hoisted.webLinksAddonDisposeSpy).toHaveBeenCalled();
  });

  it("disposes the input bridge before the xterm instance", () => {
    getOrCreateTerminal("sess-order", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    disposeTerminal("sess-order");
    expect(hoisted.bridgeDisposeSpy).toHaveBeenCalled();
    expect(hoisted.disposeSpy).toHaveBeenCalled();
    expect(hoisted.disposeOrder).toEqual(["bridge", "urlHighlighter", "webgl", "webLinks", "term"]);
  });

  // 上游 term.dispose() 虽然会级联释放已加载 addon，但 onContextLoss 返回的
  // 订阅是独立 IDisposable，不显式 dispose 会留下事件监听器引用 → 资源泄露。
  it("disposes the WebGL addon and its onContextLoss subscription before xterm dispose", () => {
    getOrCreateTerminal("sess-webgl", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    expect(hoisted.webglAddonCtor).toHaveBeenCalledTimes(1);
    disposeTerminal("sess-webgl");
    expect(hoisted.webglAddonDisposeSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.webglContextLossDisposeSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.disposeOrder.indexOf("webgl")).toBeLessThan(hoisted.disposeOrder.indexOf("term"));
  });

  it("skips WebGL when webglEnabled is false", () => {
    getOrCreateTerminal("sess-no-webgl", {
      fontSize: 14,
      fontFamily: "mono",
      scrollback: 1000,
      webglEnabled: false,
    });
    expect(hoisted.webglAddonCtor).not.toHaveBeenCalled();
    disposeTerminal("sess-no-webgl");
    expect(hoisted.webglAddonDisposeSpy).not.toHaveBeenCalled();
  });

  // #146: 右键菜单粘贴必须经 xterm 的 term.paste()——它统一做 CRLF/LF → CR 归一化
  // (replace(/\r?\n/g,"\r")) 并按 bracketed paste 包裹，与原生 Cmd/Ctrl+V 同源。
  // 旧实现把剪贴板原文(含 \r\n)直接 base64 写给后端：PTY 的 ICRNL 把每个 \r 当换行
  // 触发 `\` 续行，紧随的裸 \n 又立刻结束空续行并执行半截命令 → 多行命令被逐行拆开
  // (docker run 单独报 "requires at least 1 argument")。这里锁死"必须走 term.paste"。
  it("pasteIntoTerminal routes clipboard text through xterm term.paste (not a raw write)", () => {
    getOrCreateTerminal("sess-paste", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    const crlf = "docker run \\\r\n-v x\r\nnginx";
    pasteIntoTerminal("sess-paste", crlf);
    expect(hoisted.pasteSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.pasteSpy).toHaveBeenCalledWith(crlf);
    disposeTerminal("sess-paste");
  });

  it("pasteIntoTerminal is a no-op for an unknown session", () => {
    expect(() => pasteIntoTerminal("sess-missing", "x")).not.toThrow();
    expect(hoisted.pasteSpy).not.toHaveBeenCalled();
  });

  // 右键菜单粘贴必须经 Wails 原生 ClipboardGetText（Go 侧读系统剪贴板），
  // 不能用 navigator.clipboard.readText()——macOS WKWebView 对 JS 读剪贴板有隐私
  // 保护，会在光标处弹出系统原生「粘贴」按钮要求二次点击，而不是直接粘贴。
  // 这里锁死"必须走原生 ClipboardGetText 取文，再喂给 term.paste"。
  it("pasteFromClipboard reads via native Wails ClipboardGetText and routes through term.paste", async () => {
    getOrCreateTerminal("sess-clip", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    const clip = "docker run \\\r\n-v x\r\nnginx";
    hoisted.clipboardGetTextSpy.mockResolvedValue(clip);
    await pasteFromClipboard("sess-clip");
    expect(hoisted.clipboardGetTextSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.pasteSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.pasteSpy).toHaveBeenCalledWith(clip);
    disposeTerminal("sess-clip");
  });

  it("pasteFromClipboard can suppress the following native paste event to prevent duplicate paste", async () => {
    getOrCreateTerminal("sess-clip-suppress", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    const textarea = hoisted.state.textarea;
    expect(textarea).not.toBeNull();

    hoisted.clipboardGetTextSpy.mockResolvedValue("echo once\n");
    const pastePromise = pasteFromClipboard("sess-clip-suppress", { suppressNativePaste: true });
    const nativePasteAllowed = textarea!.dispatchEvent(new ClipboardEvent("paste", { cancelable: true }));
    await pastePromise;

    expect(nativePasteAllowed).toBe(false);
    expect(hoisted.clipboardGetTextSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.pasteSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.pasteSpy).toHaveBeenCalledWith("echo once\n");
    disposeTerminal("sess-clip-suppress");
  });

  it("pasteFromClipboard does not paste when the clipboard is empty", async () => {
    getOrCreateTerminal("sess-clip-empty", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    hoisted.clipboardGetTextSpy.mockResolvedValue("");
    await pasteFromClipboard("sess-clip-empty");
    expect(hoisted.clipboardGetTextSpy).toHaveBeenCalledTimes(1);
    expect(hoisted.pasteSpy).not.toHaveBeenCalled();
    disposeTerminal("sess-clip-empty");
  });

  it("writes terminal output bytes unchanged without injecting URL color ANSI", () => {
    const encoder = new TextEncoder();
    getOrCreateTerminal("sess-url-ansi", {
      fontSize: 14,
      fontFamily: "mono",
      scrollback: 1000,
      theme: { brightBlue: "#89b4fa" },
    });

    hoisted.eventHandlers.get("ssh:data:sess-url-ansi")?.(
      btoa(String.fromCharCode(...encoder.encode("\x1b[31mDocs: https://help.ubuntu.com suffix\x1b[0m")))
    );

    expect(hoisted.writeSpy).toHaveBeenCalledWith(
      encoder.encode("\x1b[31mDocs: https://help.ubuntu.com suffix\x1b[0m")
    );
    disposeTerminal("sess-url-ansi");
  });

  it("attaches a url highlighter and disposes it with the terminal", () => {
    getOrCreateTerminal("sess-highlight", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    expect(hoisted.attachUrlHighlighterSpy).toHaveBeenCalledTimes(1);

    disposeTerminal("sess-highlight");
    expect(hoisted.urlHighlighterDisposeSpy).toHaveBeenCalledTimes(1);
  });

  it("enables allowProposedApi so the highlighter's registerDecoration calls don't throw", () => {
    // registerDecoration / registerMarker are proposed API in xterm and throw unless
    // allowProposedApi is set — without this the link highlight silently never renders (#153).
    getOrCreateTerminal("sess-proposed", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    expect(hoisted.terminalCtor).toHaveBeenCalledWith(expect.objectContaining({ allowProposedApi: true }));
    disposeTerminal("sess-proposed");
  });

  it("re-creates a fresh terminal after dispose for the same sessionId", () => {
    const before = hoisted.terminalCtor.mock.calls.length;
    getOrCreateTerminal("sess-5", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    disposeTerminal("sess-5");
    expect(hoisted.disposeSpy).toHaveBeenCalled();
    getOrCreateTerminal("sess-5", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    expect(hoisted.terminalCtor.mock.calls.length).toBe(before + 2);
    disposeTerminal("sess-5");
  });
});
