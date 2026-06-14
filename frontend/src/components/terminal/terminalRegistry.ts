import { Terminal as XTerminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import "@xterm/xterm/css/xterm.css";
import { toast } from "sonner";
import { BrowserOpenURL, EventsOn, EventsOff, ClipboardGetText } from "../../../wailsjs/runtime/runtime";
import { bytesToBase64 } from "@/lib/terminalEncode";
import { useTerminalStore, TRANSPORTS, type TerminalTransport } from "@/stores/terminalStore";
import { useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { withTerminalFontFallback, withTerminalFontIsolation } from "@/data/terminalFonts";
import i18n from "@/i18n";
import { createTerminalInputBridge, type TerminalInputBridge } from "./terminalInputBridge";
import { createZmodemController, type ZmodemController } from "./zmodem/zmodemSession";
import { attachXtermRolloverGuard } from "./xtermRolloverGuard";
import { attachTerminalUrlHighlighter, type TerminalUrlHighlighterController } from "./terminalUrlHighlighter";
import { normalizeHttpUrl } from "./terminalUrlScan";

const PENDING_RZ_UPLOAD_TTL_MS = 10_000;

export interface TerminalInstance {
  term: XTerminal;
  fitAddon: FitAddon;
  searchAddon: SearchAddon;
  container: HTMLDivElement;
  bridge: TerminalInputBridge;
  urlHighlighter: TerminalUrlHighlighterController;
}

interface InternalInstance extends TerminalInstance {
  isClosed: boolean;
  suppressNextNativePaste: () => void;
  uploadFilesWithRz: (paths: string[]) => Promise<boolean>;
  dispose: () => void;
}

const registry = new Map<string, InternalInstance>();

export function getOrCreateTerminal(
  sessionId: string,
  init: {
    fontSize: number;
    fontFamily: string;
    theme?: ITheme;
    scrollback: number;
    transport?: TerminalTransport;
    webglEnabled?: boolean;
    highlightLinks?: boolean;
  }
): TerminalInstance {
  const cached = registry.get(sessionId);
  if (cached) return cached;

  const container = document.createElement("div");
  container.style.height = "100%";
  container.style.width = "100%";

  const resolvedFontFamily = withTerminalFontFallback(init.fontFamily);

  const term = new XTerminal({
    cursorBlink: true,
    // Required for the URL highlighter: registerDecoration is proposed API and
    // xterm throws without this, so the link tint would never render (#153).
    allowProposedApi: true,
    fontSize: init.fontSize,
    // 给每个 session 加独占 sentinel，避免 xterm 全局 CharAtlasCache 在 fontFamily/
    // fontSize/theme 相同的 terminal 之间共享 TextureAtlas（详见 withTerminalFontIsolation
    // 的注释）。共享 atlas 会让一个 session 的 clearTextureAtlas 污染所有其它 session。
    fontFamily: withTerminalFontIsolation(sessionId, resolvedFontFamily),
    theme: init.theme,
    scrollback: init.scrollback,
  });

  const fitAddon = new FitAddon();
  const searchAddon = new SearchAddon();
  const webLinksAddon = new WebLinksAddon((event, uri) => {
    if (event && event.button !== 0) return;
    const url = normalizeHttpUrl(uri);
    if (url) BrowserOpenURL(url);
  });
  term.loadAddon(fitAddon);
  term.loadAddon(searchAddon);
  term.loadAddon(webLinksAddon);
  term.open(container);
  const urlHighlighter = attachTerminalUrlHighlighter(term, {
    enabled: init.highlightLinks === true,
    color: terminalUrlHighlightColor(init.theme),
  });

  // 优先用调用方传入的 transport；首次挂载若没拿到（罕见），退回 session id 前缀。
  const transport: TerminalTransport =
    init.transport ?? (sessionId.startsWith("serial-") ? "serial" : sessionId.startsWith("local-") ? "local" : "ssh");
  const spec = TRANSPORTS[transport];
  const writeFn = spec.write;
  const eventPrefix = spec.eventPrefix;

  // 单一 keyboard 处理入口：IME 守卫 + 终端内快捷键拦截。
  // 占位回调由 Terminal.tsx 在挂载时注入。
  const bridge = createTerminalInputBridge({
    term,
    shortcuts: useShortcutStore.getState().shortcuts,
    onCopy: () => false,
    onPaste: () => {},
    onSelectAll: () => {},
    onFind: () => {},
  });

  // GPU renderer: required so customGlyphs (powerline U+E0A0–U+E0D7, box drawing)
  // is drawn by xterm instead of the system font — fixes tofu boxes from terminal
  // prompts (oh-my-zsh powerlevel10k, starship, etc.). Falls back to DOM renderer
  // automatically on context loss or if WebGL initialization throws.
  // 持有引用 + onContextLoss 订阅，instance.dispose 时显式释放 —— term.dispose
  // 虽然会级联 addon，但订阅本身是独立 IDisposable，不主动 dispose 会泄漏。
  // 失败时回写 store 的 webglEnabled=false：避免每开一个终端都重复 try/log，
  // 而且让设置面板的开关如实反映当前可用性。用户可以手动再打开重试。
  let webglAddon: WebglAddon | null = null;
  let webglContextLossSub: { dispose: () => void } | null = null;
  let webglFirstWriteSub: { dispose: () => void } | null = null;
  let webglPostWriteRenderSub: { dispose: () => void } | null = null;
  if (init.webglEnabled !== false) {
    try {
      const addon = new WebglAddon();
      webglContextLossSub = addon.onContextLoss(() => {
        addon.dispose();
        webglAddon = null;
        const store = useTerminalThemeStore.getState();
        store.reportWebglFailure({
          cause: "context-loss",
          message: "WebGL context lost",
          at: Date.now(),
        });
        store.setWebglEnabled(false);
      });
      term.loadAddon(addon);
      webglAddon = addon;
      // Wails 的 WebKit webview 上，WebGL atlas 在首次填充字形时 Canvas2D 偶尔用
      // 还没解析稳定的字体绘字，随机出现"全粗 / 全细 / 混杂"。手动切字体能恢复，
      // 是因为 addon 见到 fontFamily 变化时会清 atlas → 下一帧重新填，那时字体已
      // 稳定。我们在这里复刻同一动作：
      //   首次 onWriteParsed（数据进了 xterm）→ 下一次 onRender（数据画到了
      //   atlas）→ 立刻 clearTextureAtlas → 让后续帧用稳定状态重填。
      // 不能直接订阅 loadAddon 后的首个 onRender —— xterm 会立刻调度一帧空帧（只
      // 有光标），atlas 几乎没填，那时清掉就过早 dispose 订阅，等真正的文本数据
      // 进来时已经没人清了。
      // 安全前提：withTerminalFontIsolation 让这个 session 独占自己的 atlas，所以
      // clearTextureAtlas 不会影响其它 session（否则会污染共享 atlas 引发乱码）。
      webglFirstWriteSub = term.onWriteParsed(() => {
        webglFirstWriteSub?.dispose();
        webglFirstWriteSub = null;
        webglPostWriteRenderSub = term.onRender(() => {
          webglPostWriteRenderSub?.dispose();
          webglPostWriteRenderSub = null;
          addon.clearTextureAtlas();
        });
      });
    } catch (err) {
      const name = (err as Error)?.name;
      const message = (err as Error)?.message ?? String(err);
      const store = useTerminalThemeStore.getState();
      store.reportWebglFailure({ cause: "init-threw", name, message, at: Date.now() });
      store.setWebglEnabled(false);
    }
  }

  // ZMODEM(lrzsz) 仅在 SSH 终端启用：协议跑在前端 Sentry，截获 sz/rz 字节流并驱动
  // 上传/下载，进度复用 sftpStore 的传输 UI。serial/local 不启用，保持原样直写终端。
  const zmodem: ZmodemController | null =
    transport === "ssh"
      ? createZmodemController({
          sessionId,
          write: writeFn,
          toTerminal: (bytes) => term.write(bytes),
        })
      : null;

  const writeData = (data: string): Promise<void> => {
    // ZMODEM 传输进行中抑制键盘：协议出站字节走 Sentry 的 sender，不经 onData，
    // 所以抑制 onData 只挡用户、不挡协议。Ctrl-C 翻译成中止传输。
    if (zmodem?.isActive()) {
      if (data === "\x03") zmodem.abort();
      return Promise.resolve();
    }
    return writeFn(sessionId, bytesToBase64(new TextEncoder().encode(data))).catch(console.error);
  };

  const onDataDispose = term.onData(writeData);

  const rolloverGuard = attachXtermRolloverGuard(term, writeData);
  let suppressNativePasteUntil = 0;
  const suppressNextNativePaste = () => {
    suppressNativePasteUntil = Date.now() + 750;
  };
  const onNativePasteCapture = (event: ClipboardEvent) => {
    if (Date.now() > suppressNativePasteUntil) return;
    suppressNativePasteUntil = 0;
    event.preventDefault();
    event.stopImmediatePropagation();
  };
  term.textarea?.addEventListener("paste", onNativePasteCapture, true);

  let pendingRzUploadExpiresAt = 0;

  const uploadFilesWithRz = async (paths: string[]): Promise<boolean> => {
    if (transport !== "ssh" || !zmodem || instance.isClosed) return false;
    const uploadPaths = paths.filter(Boolean);
    if (uploadPaths.length === 0) {
      toast.warning(i18n.t("zmodem.dragNoFiles"));
      return false;
    }
    if (zmodem.isActive() || pendingRzUploadExpiresAt > Date.now()) {
      toast.warning(i18n.t("zmodem.dragBusy"));
      return false;
    }

    zmodem.queueUploadFiles(uploadPaths);
    pendingRzUploadExpiresAt = Date.now() + PENDING_RZ_UPLOAD_TTL_MS;
    try {
      await writeFn(sessionId, bytesToBase64(new TextEncoder().encode("rz\r")));
      return true;
    } catch (e) {
      zmodem.queueUploadFiles([]);
      pendingRzUploadExpiresAt = 0;
      console.error("zmodem drag upload command failed", e);
      toast.error(i18n.t("zmodem.dragCommandFailed"));
      return false;
    }
  };

  const dataEvent = `${eventPrefix}:data:${sessionId}`;
  EventsOn(dataEvent, (dataB64: string) => {
    const binary = atob(dataB64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    // SSH 走 ZMODEM Sentry：空闲时它把字节透传到终端（等价 term.write），探测到 sz/rz
    // 才截获。filterOutput 对 ZMODEM 二进制帧是非破坏性的（仅剥 token 匹配的 OSC，且只在
    // 提示符渲染时出现），故无需后端改动。详见 internal/service/ssh_svc/dirsync.go:filterOutput。
    if (zmodem) {
      zmodem.consume(bytes);
      if (zmodem.isActive()) pendingRzUploadExpiresAt = 0;
    } else {
      term.write(bytes);
    }
  });

  const closedEvent = `${eventPrefix}:closed:${sessionId}`;

  // 先声明再赋值,以便 instance.dispose 闭包可以引用 onKeyDispose
  // 而不依赖前向引用 const(可读性更好)。
  // eslint-disable-next-line prefer-const
  let onKeyDispose: { dispose: () => void };

  const instance: InternalInstance = {
    term,
    fitAddon,
    searchAddon,
    container,
    bridge,
    urlHighlighter,
    isClosed: false,
    suppressNextNativePaste,
    uploadFilesWithRz,
    dispose: () => {
      // bridge 持有 term.attachCustomKeyEventHandler 槽位的还原逻辑,
      // 必须在 term.dispose 之前调用,避免 dispose 后访问已释放对象。
      bridge.dispose();
      urlHighlighter.dispose();
      rolloverGuard.dispose();
      zmodem?.dispose();
      onDataDispose.dispose();
      onKeyDispose.dispose();
      term.textarea?.removeEventListener("paste", onNativePasteCapture, true);
      EventsOff(dataEvent);
      EventsOff(closedEvent);
      webglContextLossSub?.dispose();
      webglContextLossSub = null;
      webglFirstWriteSub?.dispose();
      webglFirstWriteSub = null;
      webglPostWriteRenderSub?.dispose();
      webglPostWriteRenderSub = null;
      webglAddon?.dispose();
      webglAddon = null;
      webLinksAddon.dispose();
      term.dispose();
      registry.delete(sessionId);
    },
  };

  onKeyDispose = term.onKey(({ key }) => {
    if (instance.isClosed && key === "\r") {
      instance.isClosed = false;
      useTerminalStore.getState().reconnectBySession(sessionId);
    }
  });

  EventsOn(closedEvent, () => {
    // 会话中途断开：中止在途 ZMODEM 传输并删除半截下载文件。
    zmodem?.abort(false);
    pendingRzUploadExpiresAt = 0;
    const hint = i18n.t("ssh.session.closedHint");
    term.write(`\r\n\x1b[31m${hint}\x1b[0m\r\n`);
    useTerminalStore.getState().markClosed(sessionId);
    instance.isClosed = true;
  });

  registry.set(sessionId, instance);
  return instance;
}

export function disposeTerminal(sessionId: string): void {
  const inst = registry.get(sessionId);
  if (inst) inst.dispose();
}

// 右键菜单粘贴统一走 xterm 的 term.paste()，与原生 Cmd/Ctrl+V 同源：它把 CRLF/LF
// 归一化成单个 \r（replace(/\r?\n/g,"\r")）并按 bracketed paste 包裹，再触发 onData →
// writeData 发往后端。绕过它直接写剪贴板原文会把 \r\n 原样送进 PTY，ICRNL 让 \r 触发
// `\` 续行、紧随的裸 \n 立刻执行半截命令，多行命令被逐行拆开（#146）。
export function pasteIntoTerminal(sessionId: string, text: string): void {
  registry.get(sessionId)?.term.paste(text);
}

// 右键菜单粘贴：从系统剪贴板取文本再喂给终端。必须走 Wails 原生 ClipboardGetText
// （Go 侧直接读系统剪贴板），不能用 navigator.clipboard.readText()：macOS 的 WKWebView
// 对 JS 读剪贴板有隐私保护，会在光标处弹出系统原生「粘贴」按钮要求再点一次，而不是
// 直接粘贴。取到文本后复用 pasteIntoTerminal，保持 #146 的 CRLF 归一化。
export async function pasteFromClipboard(sessionId: string, opts?: { suppressNativePaste?: boolean }): Promise<void> {
  if (opts?.suppressNativePaste) registry.get(sessionId)?.suppressNextNativePaste();
  const text = await ClipboardGetText();
  if (text) pasteIntoTerminal(sessionId, text);
}

export function getTerminalInstance(sessionId: string): TerminalInstance | undefined {
  return registry.get(sessionId);
}

export function uploadFilesWithRz(sessionId: string, paths: string[]): Promise<boolean> {
  return registry.get(sessionId)?.uploadFilesWithRz(paths) ?? Promise.resolve(false);
}

export function terminalUrlHighlightColor(theme: ITheme | undefined): string | undefined {
  return theme?.brightBlue ?? theme?.blue;
}
