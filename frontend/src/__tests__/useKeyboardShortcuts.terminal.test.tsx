import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac, type ShortcutBinding } from "../stores/shortcutStore";

// Build a keydown event whose modifier flags match `binding` the same way
// eventMatchesBinding() reads them, so the test works on both macOS and non-mac.
function keydownFrom(target: HTMLElement, binding: ShortcutBinding): KeyboardEvent {
  const ev = new KeyboardEvent("keydown", {
    code: binding.code,
    metaKey: isMac ? binding.mod : false,
    ctrlKey: isMac ? binding.ctrl : binding.mod,
    shiftKey: binding.shift,
    altKey: binding.alt,
    bubbles: true,
    cancelable: true,
  });
  target.dispatchEvent(ev);
  return ev;
}

// A textarea nested in an `.xterm` container mimics the xterm helper textarea
// that owns focus while typing in a terminal pane.
function makeXtermTarget(): HTMLTextAreaElement {
  const xterm = document.createElement("div");
  xterm.className = "xterm";
  const ta = document.createElement("textarea");
  xterm.appendChild(ta);
  document.body.appendChild(xterm);
  return ta;
}

function renderShortcuts() {
  const handlers = {
    onToggleAIPanel: vi.fn(),
    onToggleSidebar: vi.fn(),
    onToggleCommandPalette: vi.fn(),
  };
  renderHook(() => useKeyboardShortcuts(handlers));
  return handlers;
}

describe("useKeyboardShortcuts — terminal scope", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      isRecording: false,
      disableShortcutsInTerminal: false,
    });
  });

  it("handles app shortcuts inside the terminal when the toggle is off (current behavior)", () => {
    renderShortcuts();
    const ta = makeXtermTarget();
    const ev = keydownFrom(ta, DEFAULT_SHORTCUTS["tab.next"]);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("passes app shortcuts through to the terminal when the toggle is on", () => {
    useShortcutStore.setState({ disableShortcutsInTerminal: true });
    renderShortcuts();
    const ta = makeXtermTarget();
    const ev = keydownFrom(ta, DEFAULT_SHORTCUTS["tab.next"]);
    expect(ev.defaultPrevented).toBe(false);
  });

  it("still triggers command.quickopen inside the terminal even when the toggle is on", () => {
    useShortcutStore.setState({ disableShortcutsInTerminal: true });
    const handlers = renderShortcuts();
    const ta = makeXtermTarget();
    const ev = keydownFrom(ta, DEFAULT_SHORTCUTS["command.quickopen"]);
    expect(handlers.onToggleCommandPalette).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("leaves shortcuts outside the terminal untouched when the toggle is on", () => {
    useShortcutStore.setState({ disableShortcutsInTerminal: true });
    renderShortcuts();
    const div = document.createElement("div");
    document.body.appendChild(div);
    const ev = keydownFrom(div, DEFAULT_SHORTCUTS["tab.next"]);
    expect(ev.defaultPrevented).toBe(true);
  });
});
