import { describe, it, expect, vi, beforeEach } from "vitest";
import type { Terminal as XTerminal } from "@xterm/xterm";
import { createTerminalInputBridge } from "@/components/terminal/terminalInputBridge";
import { DEFAULT_SHORTCUTS } from "@/stores/shortcutStore";

interface FakeKeyEvent {
  type: "keydown" | "keyup";
  code: string;
  key: string;
  keyCode?: number;
  ctrlKey?: boolean;
  metaKey?: boolean;
  shiftKey?: boolean;
  altKey?: boolean;
  isComposing?: boolean;
}

function makeBridge() {
  let handler: ((e: KeyboardEvent) => boolean) | null = null;
  const term = {
    attachCustomKeyEventHandler: (fn: (e: KeyboardEvent) => boolean) => {
      handler = fn;
    },
  } as unknown as XTerminal;

  const onCopy = vi.fn(() => false);
  const onPaste = vi.fn();
  const onSelectAll = vi.fn();
  const onFind = vi.fn();

  const bridge = createTerminalInputBridge({
    term,
    shortcuts: DEFAULT_SHORTCUTS,
    onCopy,
    onPaste,
    onSelectAll,
    onFind,
  });

  const fire = (e: FakeKeyEvent): boolean => {
    if (!handler) throw new Error("handler not installed");
    const full = {
      shiftKey: false,
      altKey: false,
      ctrlKey: false,
      metaKey: false,
      isComposing: false,
      keyCode: 0,
      ...e,
    };
    return handler(full as unknown as KeyboardEvent);
  };

  const isMac = /Macintosh|Mac OS/.test(navigator.userAgent);
  const modKey: { metaKey: true } | { ctrlKey: true } = isMac ? { metaKey: true } : { ctrlKey: true };

  return { bridge, fire, onCopy, onPaste, onSelectAll, onFind, modKey, getHandler: () => handler };
}

describe("terminalInputBridge", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("returns true (lets xterm handle) when isComposing is true, even for a bound shortcut", () => {
    // terminal.find is Mod+F. While composing, the bridge must NOT call onFind
    // and must let xterm process the key so the IME commit can land.
    const { fire, onFind, modKey } = makeBridge();
    const result = fire({
      type: "keydown",
      code: "KeyF",
      key: "f",
      ...modKey,
      isComposing: true,
    });
    expect(result).toBe(true);
    expect(onFind).not.toHaveBeenCalled();
  });

  it("returns true when keyCode === 229 (Firefox IME) regardless of isComposing flag", () => {
    const { fire, onFind } = makeBridge();
    const result = fire({
      type: "keydown",
      code: "",
      key: "Process",
      keyCode: 229,
      isComposing: false,
    });
    expect(result).toBe(true);
    expect(onFind).not.toHaveBeenCalled();
  });

  it("triggers onFind and returns false on terminal.find outside composition", () => {
    const { fire, onFind, modKey } = makeBridge();
    const result = fire({
      type: "keydown",
      code: "KeyF",
      key: "f",
      ...modKey,
      isComposing: false,
    });
    expect(result).toBe(false);
    expect(onFind).toHaveBeenCalledTimes(1);
  });

  it("Ctrl+Shift+C with selection: returns false (eats the event)", () => {
    const { fire, onCopy, modKey } = makeBridge();
    onCopy.mockImplementation(() => true);
    const result = fire({ type: "keydown", code: "KeyC", key: "c", ...modKey, shiftKey: true });
    expect(result).toBe(false);
    expect(onCopy).toHaveBeenCalledTimes(1);
  });

  it("Ctrl+Shift+C without selection: returns true so xterm handles the key", () => {
    const { fire, onCopy, modKey } = makeBridge();
    onCopy.mockImplementation(() => false);
    const result = fire({ type: "keydown", code: "KeyC", key: "c", ...modKey, shiftKey: true });
    expect(result).toBe(true);
    expect(onCopy).toHaveBeenCalledTimes(1);
  });

  it("plain Ctrl+C returns true so xterm sends SIGINT", () => {
    const { fire, onCopy, modKey } = makeBridge();
    onCopy.mockImplementation(() => true);
    const result = fire({ type: "keydown", code: "KeyC", key: "c", ...modKey });
    expect(result).toBe(true);
    expect(onCopy).not.toHaveBeenCalled();
  });

  it("setShortcuts hot-updates the binding used by the handler", () => {
    const { bridge, fire, onFind } = makeBridge();
    // Remap terminal.find from KeyF to KeyG.
    bridge.setShortcuts({
      ...DEFAULT_SHORTCUTS,
      "terminal.find": { code: "KeyG", mod: true, ctrl: false, shift: false, alt: false },
    });
    const isMac = /Macintosh|Mac OS/.test(navigator.userAgent);
    const mod = isMac ? { metaKey: true } : { ctrlKey: true };

    // Old binding no longer fires onFind.
    expect(fire({ type: "keydown", code: "KeyF", key: "f", ...mod })).toBe(true);
    expect(onFind).not.toHaveBeenCalled();

    // New binding does.
    expect(fire({ type: "keydown", code: "KeyG", key: "g", ...mod })).toBe(false);
    expect(onFind).toHaveBeenCalledTimes(1);
  });

  it("remapping terminal.copy makes the old Ctrl+Shift+C default pass through", () => {
    const { bridge, fire, onCopy, modKey } = makeBridge();
    onCopy.mockImplementation(() => true);
    bridge.setShortcuts({
      ...DEFAULT_SHORTCUTS,
      "terminal.copy": { code: "KeyY", mod: true, ctrl: false, shift: false, alt: false },
    });

    expect(fire({ type: "keydown", code: "KeyC", key: "c", ...modKey, shiftKey: true })).toBe(true);
    expect(onCopy).not.toHaveBeenCalled();

    expect(fire({ type: "keydown", code: "KeyY", key: "y", ...modKey })).toBe(false);
    expect(onCopy).toHaveBeenCalledTimes(1);
  });

  it("remapping terminal.copy does not change paste/select all/find bindings", () => {
    const { bridge, fire, onCopy, onPaste, onSelectAll, onFind, modKey } = makeBridge();
    onCopy.mockImplementation(() => true);
    bridge.setShortcuts({
      ...DEFAULT_SHORTCUTS,
      "terminal.copy": { code: "KeyY", mod: true, ctrl: false, shift: false, alt: false },
    });

    expect(fire({ type: "keydown", code: "KeyV", key: "v", ...modKey, shiftKey: true })).toBe(false);
    expect(onPaste).toHaveBeenCalledTimes(1);

    expect(fire({ type: "keydown", code: "KeyA", key: "a", ...modKey })).toBe(false);
    expect(onSelectAll).toHaveBeenCalledTimes(1);

    expect(fire({ type: "keydown", code: "KeyF", key: "f", ...modKey })).toBe(false);
    expect(onFind).toHaveBeenCalledTimes(1);
    expect(onCopy).not.toHaveBeenCalled();
  });

  it("triggers terminal paste callback for the default Ctrl+Shift+V binding", () => {
    const { fire, onPaste, modKey } = makeBridge();

    expect(fire({ type: "keydown", code: "KeyV", key: "v", ...modKey, shiftKey: true })).toBe(false);
    expect(onPaste).toHaveBeenCalledTimes(1);
  });

  it("plain Ctrl+V returns true so xterm handles the key", () => {
    const { fire, onPaste, modKey } = makeBridge();

    expect(fire({ type: "keydown", code: "KeyV", key: "v", ...modKey })).toBe(true);
    expect(onPaste).not.toHaveBeenCalled();
  });

  it("triggers terminal paste callback for a remapped non-native paste binding", () => {
    const { bridge, fire, onPaste, modKey } = makeBridge();
    bridge.setShortcuts({
      ...DEFAULT_SHORTCUTS,
      "terminal.paste": { code: "KeyY", mod: true, ctrl: false, shift: false, alt: false },
    });

    expect(fire({ type: "keydown", code: "KeyY", key: "y", ...modKey })).toBe(false);
    expect(onPaste).toHaveBeenCalledTimes(1);
  });

  it("triggers terminal select all/find callbacks", () => {
    const { fire, onPaste, onSelectAll, onFind, modKey } = makeBridge();

    expect(fire({ type: "keydown", code: "KeyA", key: "a", ...modKey })).toBe(false);
    expect(onSelectAll).toHaveBeenCalledTimes(1);

    expect(fire({ type: "keydown", code: "KeyF", key: "f", ...modKey })).toBe(false);
    expect(onFind).toHaveBeenCalledTimes(1);
    expect(onPaste).not.toHaveBeenCalled();
  });

  it("dispose restores handler to a no-op pass-through; subsequent shortcut keys do nothing", () => {
    const { bridge, fire, onFind, modKey } = makeBridge();
    bridge.dispose();
    const result = fire({
      type: "keydown",
      code: "KeyF",
      key: "f",
      ...modKey,
    });
    expect(result).toBe(true);
    expect(onFind).not.toHaveBeenCalled();
  });
});
