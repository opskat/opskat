/** Shared, side-effect-free helpers for the RDP viewer panel. */

/** RDP keyboard scancodes (set 1). Delete uses the numpad code (0x53), which is
 * the classic Secure Attention Sequence code for Ctrl+Alt+Del. */
export const SCANCODE = {
  ctrl: 0x1d,
  alt: 0x38,
  del: 0x53,
  esc: 0x01,
  tab: 0x0f,
  enter: 0x1c,
  backspace: 0x0e,
  space: 0x39,
  v: 0x2f,
} as const;

/** Physical-key (KeyboardEvent.code) → non-extended RDP scancode (set 1), keyed
 * by *location* so it is keyboard-layout independent. Extended (E0-prefixed)
 * keys live in EXTENDED_SCANCODES; resolve either through scancodeFor. */
export const KEY_SCANCODES: Record<string, number> = {
  // Modifiers. AltRight/AltGr is left out to preserve Unicode composition of
  // AltGr characters; right Ctrl is an extended key (see EXTENDED_SCANCODES).
  ControlLeft: SCANCODE.ctrl,
  ShiftLeft: 0x2a,
  ShiftRight: 0x36,
  AltLeft: SCANCODE.alt,
  CapsLock: 0x3a,
  // Editing / whitespace.
  Escape: SCANCODE.esc,
  Tab: SCANCODE.tab,
  Enter: SCANCODE.enter,
  Backspace: SCANCODE.backspace,
  Space: SCANCODE.space,
  // Letters.
  KeyA: 0x1e,
  KeyB: 0x30,
  KeyC: 0x2e,
  KeyD: 0x20,
  KeyE: 0x12,
  KeyF: 0x21,
  KeyG: 0x22,
  KeyH: 0x23,
  KeyI: 0x17,
  KeyJ: 0x24,
  KeyK: 0x25,
  KeyL: 0x26,
  KeyM: 0x32,
  KeyN: 0x31,
  KeyO: 0x18,
  KeyP: 0x19,
  KeyQ: 0x10,
  KeyR: 0x13,
  KeyS: 0x1f,
  KeyT: 0x14,
  KeyU: 0x16,
  KeyV: SCANCODE.v,
  KeyW: 0x11,
  KeyX: 0x2d,
  KeyY: 0x15,
  KeyZ: 0x2c,
  // Top-row digits.
  Digit1: 0x02,
  Digit2: 0x03,
  Digit3: 0x04,
  Digit4: 0x05,
  Digit5: 0x06,
  Digit6: 0x07,
  Digit7: 0x08,
  Digit8: 0x09,
  Digit9: 0x0a,
  Digit0: 0x0b,
  // Punctuation.
  Minus: 0x0c,
  Equal: 0x0d,
  BracketLeft: 0x1a,
  BracketRight: 0x1b,
  Backslash: 0x2b,
  Semicolon: 0x27,
  Quote: 0x28,
  Backquote: 0x29,
  Comma: 0x33,
  Period: 0x34,
  Slash: 0x35,
  // Function keys (all non-extended in set 1).
  F1: 0x3b,
  F2: 0x3c,
  F3: 0x3d,
  F4: 0x3e,
  F5: 0x3f,
  F6: 0x40,
  F7: 0x41,
  F8: 0x42,
  F9: 0x43,
  F10: 0x44,
  F11: 0x57,
  F12: 0x58,
};

/** Physical-key → *extended* (E0-prefixed set-1) base scancode. These reach the
 * remote as the base value plus the KBDFLAGS_EXTENDED flag (handled by the Go
 * SendKeyboardExtended path). AltRight/AltGr and the Win keys are omitted on
 * purpose (AltGr feeds Unicode composition; Win is a host shortcut). */
export const EXTENDED_SCANCODES: Record<string, number> = {
  ControlRight: SCANCODE.ctrl,
  ArrowUp: 0x48,
  ArrowDown: 0x50,
  ArrowLeft: 0x4b,
  ArrowRight: 0x4d,
  Home: 0x47,
  End: 0x4f,
  PageUp: 0x49,
  PageDown: 0x51,
  Insert: 0x52,
  Delete: 0x53,
  NumpadEnter: SCANCODE.enter,
};

/** Resolve a physical key to its scancode and whether it is an extended key.
 * Returns undefined for keys with no scancode mapping (e.g. IME/dead keys). */
export function scancodeFor(code: string): { scancode: number; extended?: boolean } | undefined {
  const ext = EXTENDED_SCANCODES[code];
  if (ext !== undefined) return { scancode: ext, extended: true };
  const base = KEY_SCANCODES[code];
  if (base !== undefined) return { scancode: base };
  return undefined;
}

/** Keys that go to the remote as scancodes even with no modifier held: they are
 * control / whitespace / navigation / function keys, not typed text. Modifiers
 * are here so the remote sees Ctrl/Alt/Shift held while composing a shortcut;
 * Meta (Win/Cmd) is deliberately excluded — it is reserved for the host's own
 * shortcuts (e.g. Cmd+V local-clipboard paste). */
const SCANCODE_ONLY_CODES = new Set([
  "ControlLeft",
  "ControlRight",
  "ShiftLeft",
  "ShiftRight",
  "AltLeft",
  "CapsLock",
  "Escape",
  "Tab",
  "Enter",
  "Backspace",
  "Space",
  "F1",
  "F2",
  "F3",
  "F4",
  "F5",
  "F6",
  "F7",
  "F8",
  "F9",
  "F10",
  "F11",
  "F12",
  "ArrowUp",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "Home",
  "End",
  "PageUp",
  "PageDown",
  "Insert",
  "Delete",
  "NumpadEnter",
]);

export type KeyPlan =
  | { kind: "scancode"; scancode: number; extended?: boolean }
  | { kind: "unicode"; codepoint: number }
  | { kind: "none" };

/** Decide how a keydown should reach the remote. Chords (Ctrl/Alt held) and
 * control/navigation/function keys go as scancodes — RDP Unicode events cannot
 * carry modifier state, so a shortcut only works as scancodes. Plain printable
 * characters go as Unicode, which is layout- and IME-robust. */
export function planKeyDown(code: string, key: string, mods: { ctrl: boolean; alt: boolean; meta: boolean }): KeyPlan {
  const sc = scancodeFor(code);
  if (sc !== undefined && (mods.ctrl || mods.alt || SCANCODE_ONLY_CODES.has(code))) {
    return { kind: "scancode", scancode: sc.scancode, extended: sc.extended };
  }
  if (key.length === 1 && !mods.ctrl && !mods.alt && !mods.meta) {
    return { kind: "unicode", codepoint: key.charCodeAt(0) };
  }
  return { kind: "none" };
}

/** MS-RDPEDISP accepts dimensions down to 200px. Clamp only to the protocol
 * floor so Fit mode preserves the viewport aspect ratio without letterboxing. */
export const MIN_REMOTE_WIDTH = 200;
export const MIN_REMOTE_HEIGHT = 200;

/** Round a viewport rect to integer pixels and clamp to the RDP minimum. */
export function clampRemoteSize(width: number, height: number): { width: number; height: number } {
  return {
    width: Math.max(MIN_REMOTE_WIDTH, Math.round(width)),
    height: Math.max(MIN_REMOTE_HEIGHT, Math.round(height)),
  };
}

interface Point {
  clientX: number;
  clientY: number;
}

interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}

interface Size {
  width: number;
  height: number;
}

interface RemotePoint {
  x: number;
  y: number;
}

/** Map a pointer from the canvas border box to the remote framebuffer.
 * Fit mode uses object-fit: contain, so the rendered image may be inset by
 * horizontal or vertical letterboxing inside that border box. */
export function remotePoint(point: Point, rect: Rect, remote: Size, viewMode: "fit" | "actual"): RemotePoint {
  let renderedWidth = rect.width;
  let renderedHeight = rect.height;
  let renderedLeft = rect.left;
  let renderedTop = rect.top;

  if (viewMode === "fit") {
    const scale = Math.min(rect.width / remote.width, rect.height / remote.height);
    renderedWidth = remote.width * scale;
    renderedHeight = remote.height * scale;
    renderedLeft += (rect.width - renderedWidth) / 2;
    renderedTop += (rect.height - renderedHeight) / 2;
  }

  return {
    x: Math.max(
      0,
      Math.min(remote.width - 1, Math.round(((point.clientX - renderedLeft) / renderedWidth) * remote.width))
    ),
    y: Math.max(
      0,
      Math.min(remote.height - 1, Math.round(((point.clientY - renderedTop) / renderedHeight) * remote.height))
    ),
  };
}

/** A chorded shortcut: press every key in order, then release them in reverse. */
export function chordSequence(scancodes: number[]): { scancode: number; pressed: boolean }[] {
  const down = scancodes.map((scancode) => ({ scancode, pressed: true }));
  const up = [...scancodes].reverse().map((scancode) => ({ scancode, pressed: false }));
  return [...down, ...up];
}

/** Format elapsed seconds as HH:MM:SS. */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}
