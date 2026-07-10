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
} as const;

/** gopher-rdp reconnects when resized; keep the remote at a sane floor so a
 * collapsed/hidden viewport never asks for a 0×0 (or tiny) desktop. */
export const MIN_REMOTE_WIDTH = 1024;
export const MIN_REMOTE_HEIGHT = 720;

/** Round a viewport rect to integer pixels and clamp to the RDP minimum. */
export function clampRemoteSize(width: number, height: number): { width: number; height: number } {
  return {
    width: Math.max(MIN_REMOTE_WIDTH, Math.round(width)),
    height: Math.max(MIN_REMOTE_HEIGHT, Math.round(height)),
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
