import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import "@xterm/xterm/css/xterm.css";
import { ResizeK8sExec, StartK8sExec, StopK8sExec, WriteK8sExec } from "../../../../wailsjs/go/k8s/K8s";
import { EventsOff, EventsOn } from "../../../../wailsjs/runtime/runtime";
import i18n from "@/i18n";
import { errorMessage } from "@/lib/errorMessage";
import { createOrderedQueue } from "@/lib/orderedQueue";
import { bytesToBase64 } from "@/lib/terminalEncode";
import { withTerminalFontFallback, withTerminalFontIsolation } from "@/data/terminalFonts";
import { useK8sStore, type K8sTerminalEnd } from "@/stores/k8sStore";
import { useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { createTerminalInputBridge, type TerminalInputBridge } from "@/components/terminal/terminalInputBridge";
import { base64ToBytes } from "../utils";

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
  sessionId: string | null;
  disposed: boolean;
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

  const entry: Entry = { assetId, tabId, term, fitAddon, searchAddon, bridge, host, sessionId: null, disposed: false };
  entries.set(key, entry);

  // Wails runs every IPC call on its own goroutine, so keystrokes are chained to arrive in order.
  const writes = createOrderedQueue();
  term.onData((data) => {
    const id = entry.sessionId;
    if (!id) return;
    void writes
      .push(() => WriteK8sExec(id, bytesToBase64(new TextEncoder().encode(data))))
      .catch((e) => console.error("k8s exec write failed", e));
  });

  const update = useK8sStore.getState().updateTerminal;
  const finish = (end: K8sTerminalEnd) => {
    term.write(`\r\n\x1b[90m${endLine(end)}\x1b[0m\r\n`);
    update(assetId, tabId, { status: "ended", end });
  };

  StartK8sExec({
    assetId,
    namespace: spec.namespace,
    pod: spec.pod,
    container: spec.container,
    shell: spec.shell,
    cols: term.cols,
    rows: term.rows,
  } as never).then(
    (id) => {
      if (entry.disposed) {
        void StopK8sExec(id);
        return;
      }
      entry.sessionId = id;
      EventsOn(`k8s:exec:data:${id}`, (dataB64: string) => term.write(base64ToBytes(dataB64)));
      EventsOn(`k8s:exec:end:${id}`, (end: K8sTerminalEnd) => {
        unsubscribe(id);
        entry.sessionId = null;
        finish(end);
      });
      update(assetId, tabId, { status: "connected" });
      // The tab may have fitted the terminal while the session was connecting.
      void ResizeK8sExec(id, term.cols, term.rows).catch((e) => console.error("k8s exec resize failed", e));
    },
    (e) => {
      if (entry.disposed) return;
      finish({ reason: "error", error: errorMessage(e) });
    }
  );
}

function endLine(end: K8sTerminalEnd): string {
  if (end.reason === "exited") return i18n.t("asset.k8sTerminalExited", { code: end.exitCode ?? 0 });
  if (end.reason === "error") return i18n.t("asset.k8sTerminalFailed", { error: end.error ?? "" });
  return i18n.t("asset.k8sTerminalDisconnected");
}

function unsubscribe(sessionId: string) {
  EventsOff(`k8s:exec:data:${sessionId}`);
  EventsOff(`k8s:exec:end:${sessionId}`);
}

/** Fits the terminal to its host and sends the new size to the container. */
export function fitPodTerminal(assetId: number, tabId: string) {
  const entry = entries.get(keyOf(assetId, tabId));
  if (!entry) return;
  entry.fitAddon.fit();
  const { cols, rows } = entry.term;
  if (entry.sessionId && cols > 0 && rows > 0) {
    void ResizeK8sExec(entry.sessionId, cols, rows).catch((e) => console.error("k8s exec resize failed", e));
  }
}

function dispose(entry: Entry) {
  entry.disposed = true;
  entries.delete(keyOf(entry.assetId, entry.tabId));
  if (entry.sessionId) {
    unsubscribe(entry.sessionId);
    void StopK8sExec(entry.sessionId);
  }
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
