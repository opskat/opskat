import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import "@xterm/xterm/css/xterm.css";
import { ResizeK8sExec, StartK8sExec, StopK8sExec, WriteK8sExec } from "../../../../wailsjs/go/k8s/K8s";
import { EventsOff, EventsOn } from "../../../../wailsjs/runtime/runtime";
import { errorMessage } from "@/lib/errorMessage";
import { createOrderedQueue } from "@/lib/orderedQueue";
import { bytesToBase64 } from "@/lib/terminalEncode";
import { withTerminalFontFallback, withTerminalFontIsolation } from "@/data/terminalFonts";
import { useK8sStore, type K8sTerminalEnd, type K8sTerminalState } from "@/stores/k8sStore";
import { useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { createTerminalInputBridge, type TerminalInputBridge } from "@/components/terminal/terminalInputBridge";
import { base64ToBytes } from "../utils";
import { endText, separatorText, startedText } from "./terminalText";

/**
 * Pod terminals live here, outside React: the cluster page unmounts on every top-level tab switch and the
 * active inner tab is the only one rendered, so the xterm (with its scrollback) and the exec session must
 * outlive PodTerminalTab. The tab mounts `host` into its layout and detaches it on unmount. A terminal ends
 * when its entry leaves useK8sStore().terminals — its tab closed or the cluster tab closed.
 */
export interface PodTerminal {
  term: XTerminal;
  fitAddon: FitAddon;
  searchAddon: SearchAddon;
  bridge: TerminalInputBridge;
  /** The element xterm renders into; PodTerminalTab moves it in and out of the page. */
  host: HTMLDivElement;
}

interface Entry extends PodTerminal {
  assetId: number;
  tabId: string;
  /** The current session's id (chosen here, so its events are subscribed before the backend can emit any). */
  sessionId: string | null;
  /** The backend confirmed the session (StartK8sExec returned), so StopK8sExec has something to stop. */
  live: boolean;
}

const entries = new Map<string, Entry>();
const keyOf = (assetId: number, tabId: string) => `${assetId}|${tabId}`;

export function getPodTerminal(assetId: number, tabId: string): PodTerminal | undefined {
  return entries.get(keyOf(assetId, tabId));
}

/** Creates the xterm of a terminal tab opened with useK8sStore().openTerminal and starts its exec session. */
export function startPodTerminal(assetId: number, tabId: string) {
  const spec = useK8sStore.getState().terminals[assetId]?.[tabId];
  const key = keyOf(assetId, tabId);
  if (!spec || entries.has(key)) return;

  const { fontSize, fontFamily, scrollback } = useTerminalThemeStore.getState();
  const host = document.createElement("div");
  host.style.height = "100%";
  host.style.width = "100%";
  const term = new XTerminal({
    cursorBlink: true,
    fontSize,
    // A per-terminal sentinel keeps xterm from sharing its glyph atlas between terminals (see terminalFonts).
    fontFamily: withTerminalFontIsolation(key, withTerminalFontFallback(fontFamily)),
    scrollback,
  });
  const fitAddon = new FitAddon();
  const searchAddon = new SearchAddon();
  term.loadAddon(fitAddon);
  term.loadAddon(searchAddon);
  term.open(host);
  // Copy / paste / find handlers are filled in by PodTerminalTab while it is mounted.
  const bridge = createTerminalInputBridge({
    term,
    shortcuts: useShortcutStore.getState().shortcuts,
    onCopy: () => false,
    onPaste: () => {},
    onSelectAll: () => {},
    onFind: () => {},
  });

  const entry: Entry = {
    assetId,
    tabId,
    term,
    fitAddon,
    searchAddon,
    bridge,
    host,
    sessionId: null,
    live: false,
  };
  entries.set(key, entry);

  // Wails runs every IPC call on its own goroutine, so keystrokes are chained to arrive in order.
  const writes = createOrderedQueue();
  term.onData((data) => {
    const id = entry.sessionId;
    if (!id || !entry.live) return;
    void writes
      .push(() => WriteK8sExec(id, bytesToBase64(new TextEncoder().encode(data))))
      .catch((e) => console.error("k8s exec write failed", e));
  });

  connect(entry);
}

function spec(entry: Entry): K8sTerminalState | undefined {
  return useK8sStore.getState().terminals[entry.assetId]?.[entry.tabId];
}

function finish(entry: Entry, end: K8sTerminalEnd) {
  entry.term.write(`\r\n\x1b[90m${endText(end)}\x1b[0m\r\n`);
  useK8sStore.getState().updateTerminal(entry.assetId, entry.tabId, { status: "ended", end });
}

/**
 * Opens a session with the terminal's current container / shell. Its events are subscribed before the exec
 * request goes out: the backend emits straight from its exec goroutine, so the first output can arrive before
 * the reply to StartK8sExec does.
 */
function connect(entry: Entry) {
  const state = spec(entry);
  if (!state) return;
  const { assetId, tabId, term } = entry;
  const sessionId = `k8s-exec-${crypto.randomUUID()}`;
  entry.sessionId = sessionId;
  entry.live = false;
  const update = useK8sStore.getState().updateTerminal;
  const current = () => entry.sessionId === sessionId;

  EventsOn(`k8s:exec:data:${sessionId}`, (dataB64: string) => {
    if (current()) term.write(base64ToBytes(dataB64));
  });
  EventsOn(`k8s:exec:started:${sessionId}`, (started: { shell: string }) => {
    if (!current()) return;
    term.write(`\x1b[90m${startedText(state, started.shell)}\x1b[0m\r\n`);
    update(assetId, tabId, { shellUsed: started.shell });
  });
  EventsOn(`k8s:exec:end:${sessionId}`, (end: K8sTerminalEnd) => {
    if (!current()) return;
    unsubscribe(sessionId);
    entry.sessionId = null;
    entry.live = false;
    finish(entry, end);
  });

  StartK8sExec({
    sessionId,
    assetId,
    namespace: state.namespace,
    pod: state.pod,
    container: state.container,
    shell: state.shell,
    cols: term.cols,
    rows: term.rows,
  } as never).then(
    () => {
      if (!current()) {
        // Replaced or closed while connecting.
        void StopK8sExec(sessionId);
        return;
      }
      entry.live = true;
      update(assetId, tabId, { status: "connected" });
      // The tab may have fitted the terminal while the session was connecting.
      void ResizeK8sExec(sessionId, term.cols, term.rows).catch((e) => console.error("k8s exec resize failed", e));
    },
    (e) => {
      if (!current()) return;
      unsubscribe(sessionId);
      entry.sessionId = null;
      finish(entry, { reason: "error", error: errorMessage(e), startFailed: true });
    }
  );
}

function unsubscribe(sessionId: string) {
  EventsOff(`k8s:exec:data:${sessionId}`);
  EventsOff(`k8s:exec:started:${sessionId}`);
  EventsOff(`k8s:exec:end:${sessionId}`);
}

/** Drops the current session without reporting an end: its events are unsubscribed and the connection stopped. */
function release(entry: Entry) {
  const id = entry.sessionId;
  if (!id) return;
  entry.sessionId = null;
  unsubscribe(id);
  // A session still connecting is stopped when its StartK8sExec reply arrives and finds it replaced.
  if (entry.live) void StopK8sExec(id);
  entry.live = false;
}

/**
 * Ends the current session (if any) and starts a new one in the same terminal, optionally with another
 * container / shell. The output stays; a separator marks where the new session begins.
 */
export function restartPodTerminal(
  assetId: number,
  tabId: string,
  change: { container?: string; shell?: string } = {}
) {
  const entry = entries.get(keyOf(assetId, tabId));
  if (!entry) return;
  release(entry);
  const update = useK8sStore.getState().updateTerminal;
  update(assetId, tabId, { ...change, shellUsed: null, status: "connecting", end: null });
  const state = spec(entry);
  if (!state) return;
  entry.term.write(`\r\n\x1b[90m${separatorText(state.container)}\x1b[0m\r\n`);
  connect(entry);
}

/** Disconnects the session; the terminal keeps its output and shows it ended. */
export function disconnectPodTerminal(assetId: number, tabId: string) {
  const entry = entries.get(keyOf(assetId, tabId));
  if (!entry?.sessionId) return;
  if (entry.live) {
    // The backend reports the end (reason "closed") once the connection is released.
    void StopK8sExec(entry.sessionId);
    return;
  }
  release(entry);
  finish(entry, { reason: "closed" });
}

/** Fits the terminal to its host and sends the new size to the container. */
export function fitPodTerminal(assetId: number, tabId: string) {
  const entry = entries.get(keyOf(assetId, tabId));
  if (!entry) return;
  entry.fitAddon.fit();
  const { cols, rows } = entry.term;
  if (entry.sessionId && entry.live && cols > 0 && rows > 0) {
    void ResizeK8sExec(entry.sessionId, cols, rows).catch((e) => console.error("k8s exec resize failed", e));
  }
}

function dispose(entry: Entry) {
  entries.delete(keyOf(entry.assetId, entry.tabId));
  release(entry);
  entry.bridge.dispose();
  entry.term.dispose();
  entry.host.remove();
}

// A terminal whose state left the store (its tab closed, or the whole cluster tab closed) ends its session.
useK8sStore.subscribe((state) => {
  for (const entry of [...entries.values()]) {
    if (!state.terminals[entry.assetId]?.[entry.tabId]) dispose(entry);
  }
});
