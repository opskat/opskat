import type { Terminal as XTerminal } from "@xterm/xterm";
import { eventMatchesBinding, type ShortcutAction, type ShortcutBinding } from "@/stores/shortcutStore";

export interface TerminalInputBridgeOptions {
  term: XTerminal;
  shortcuts: Record<ShortcutAction, ShortcutBinding>;
  onCopy: () => boolean;
  onPaste: () => void;
  onSelectAll: () => void;
  onFind: () => void;
}

export interface TerminalInputBridge {
  setShortcuts(s: Record<ShortcutAction, ShortcutBinding>): void;
  setOnCopy(cb: () => boolean): void;
  setOnPaste(cb: () => void): void;
  setOnSelectAll(cb: () => void): void;
  setOnFind(cb: () => void): void;
  dispose(): void;
}

export function createTerminalInputBridge(opts: TerminalInputBridgeOptions): TerminalInputBridge {
  const { term } = opts;
  let shortcuts = opts.shortcuts;
  let onCopy = opts.onCopy;
  let onPaste = opts.onPaste;
  let onSelectAll = opts.onSelectAll;
  let onFind = opts.onFind;
  let disposed = false;

  const handler = (e: KeyboardEvent): boolean => {
    if (disposed) return true;
    // W3C UI Events §5.4.3: 应用层在 IME composition / keyCode=229 时必须早返回，
    // 否则可能误吞掉应交给 IME / xterm 内部处理的按键，导致字符丢失。
    if (e.isComposing || e.keyCode === 229) return true;

    if (e.type !== "keydown") return true;

    if (eventMatchesBinding(e, shortcuts["terminal.copy"])) {
      return !onCopy();
    }
    if (eventMatchesBinding(e, shortcuts["terminal.paste"])) {
      onPaste();
      return false;
    }
    if (eventMatchesBinding(e, shortcuts["terminal.selectAll"])) {
      onSelectAll();
      return false;
    }
    if (eventMatchesBinding(e, shortcuts["terminal.find"])) {
      onFind();
      return false;
    }
    return true;
  };

  term.attachCustomKeyEventHandler(handler);

  return {
    setShortcuts(s) {
      shortcuts = s;
    },
    setOnCopy(cb) {
      onCopy = cb;
    },
    setOnPaste(cb) {
      onPaste = cb;
    },
    setOnSelectAll(cb) {
      onSelectAll = cb;
    },
    setOnFind(cb) {
      onFind = cb;
    },
    dispose() {
      disposed = true;
      term.attachCustomKeyEventHandler(() => true);
    },
  };
}
