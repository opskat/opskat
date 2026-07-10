import { describe, it, expect } from "vitest";
import {
  clampRemoteSize,
  chordSequence,
  formatDuration,
  planKeyDown,
  remotePoint,
  scancodeFor,
  EXTENDED_SCANCODES,
  KEY_SCANCODES,
  SCANCODE,
  MIN_REMOTE_WIDTH,
  MIN_REMOTE_HEIGHT,
} from "../rdpInput";

const NO_MODS = { ctrl: false, alt: false, meta: false };

describe("clampRemoteSize", () => {
  it("rounds fractional viewport dimensions to whole pixels", () => {
    expect(clampRemoteSize(1600.4, 900.6)).toEqual({ width: 1600, height: 901 });
  });

  it("clamps dimensions below the RDP minimum up to the minimum", () => {
    expect(clampRemoteSize(100, 100)).toEqual({ width: MIN_REMOTE_WIDTH, height: MIN_REMOTE_HEIGHT });
  });

  it("keeps a short viewport's aspect ratio instead of forcing a 720px-high desktop", () => {
    expect(clampRemoteSize(1600, 560)).toEqual({ width: 1600, height: 560 });
  });
});

describe("remotePoint", () => {
  it("subtracts horizontal letterboxing in fit mode before mapping the pointer", () => {
    expect(
      remotePoint(
        { clientX: 250, clientY: 225 },
        { left: 0, top: 0, width: 800, height: 450 },
        { width: 1024, height: 768 },
        "fit"
      )
    ).toEqual({ x: 256, y: 384 });
  });

  it("subtracts vertical letterboxing in fit mode before mapping the pointer", () => {
    expect(
      remotePoint(
        { clientX: 400, clientY: 187.5 },
        { left: 0, top: 0, width: 800, height: 600 },
        { width: 1280, height: 720 },
        "fit"
      )
    ).toEqual({ x: 640, y: 180 });
  });
});

describe("chordSequence", () => {
  it("presses keys in order then releases them in reverse (Ctrl+Alt+Del)", () => {
    expect(chordSequence([SCANCODE.ctrl, SCANCODE.alt, SCANCODE.del])).toEqual([
      { scancode: SCANCODE.ctrl, pressed: true },
      { scancode: SCANCODE.alt, pressed: true },
      { scancode: SCANCODE.del, pressed: true },
      { scancode: SCANCODE.del, pressed: false },
      { scancode: SCANCODE.alt, pressed: false },
      { scancode: SCANCODE.ctrl, pressed: false },
    ]);
  });

  it("uses the numpad Delete scancode (0x53) — the classic secure-attention code", () => {
    expect(SCANCODE.del).toBe(0x53);
    expect(SCANCODE.ctrl).toBe(0x1d);
    expect(SCANCODE.alt).toBe(0x38);
  });
});

describe("planKeyDown", () => {
  it("sends a Ctrl chord letter as its physical scancode, not Unicode", () => {
    // Ctrl+C must reach the remote as scancodes; a Unicode 'c' can't carry Ctrl.
    expect(planKeyDown("KeyC", "c", { ...NO_MODS, ctrl: true })).toEqual({
      kind: "scancode",
      scancode: KEY_SCANCODES.KeyC,
    });
  });

  it("sends the modifier key itself as a scancode so the remote sees it held", () => {
    expect(planKeyDown("ControlLeft", "Control", { ...NO_MODS, ctrl: true })).toEqual({
      kind: "scancode",
      scancode: SCANCODE.ctrl,
    });
    expect(planKeyDown("AltLeft", "Alt", { ...NO_MODS, alt: true })).toEqual({
      kind: "scancode",
      scancode: SCANCODE.alt,
    });
    expect(planKeyDown("ShiftLeft", "Shift", NO_MODS)).toEqual({
      kind: "scancode",
      scancode: KEY_SCANCODES.ShiftLeft,
    });
  });

  it("sends an Alt chord letter as a scancode", () => {
    expect(planKeyDown("KeyF", "f", { ...NO_MODS, alt: true })).toEqual({
      kind: "scancode",
      scancode: KEY_SCANCODES.KeyF,
    });
  });

  it("sends editing keys as scancodes even with no modifier held", () => {
    expect(planKeyDown("Enter", "Enter", NO_MODS)).toEqual({ kind: "scancode", scancode: SCANCODE.enter });
    expect(planKeyDown("Backspace", "Backspace", NO_MODS)).toEqual({ kind: "scancode", scancode: SCANCODE.backspace });
    expect(planKeyDown("Escape", "Escape", NO_MODS)).toEqual({ kind: "scancode", scancode: SCANCODE.esc });
    expect(planKeyDown("Tab", "Tab", NO_MODS)).toEqual({ kind: "scancode", scancode: SCANCODE.tab });
    expect(planKeyDown("Space", " ", NO_MODS)).toEqual({ kind: "scancode", scancode: SCANCODE.space });
  });

  it("sends function keys as scancodes", () => {
    expect(planKeyDown("F5", "F5", NO_MODS)).toEqual({ kind: "scancode", scancode: KEY_SCANCODES.F5 });
  });

  it("types a plain printable character as Unicode (layout/IME robust)", () => {
    expect(planKeyDown("KeyA", "a", NO_MODS)).toEqual({ kind: "unicode", codepoint: 97 });
    // Shift-for-capitalization is already reflected in e.key, so Unicode still applies.
    expect(planKeyDown("KeyA", "A", NO_MODS)).toEqual({ kind: "unicode", codepoint: 65 });
  });

  it("ignores a Meta (Cmd/Win) combo that is neither a chord nor printable text", () => {
    // Cmd is reserved for local shortcuts; it must not smuggle a scancode or Unicode char.
    expect(planKeyDown("KeyA", "a", { ...NO_MODS, meta: true })).toEqual({ kind: "none" });
  });

  it("sends arrow keys as extended scancodes even with no modifier held", () => {
    // Arrows are E0-prefixed set-1 keys: base scancode + the extended flag.
    expect(planKeyDown("ArrowRight", "ArrowRight", NO_MODS)).toEqual({
      kind: "scancode",
      scancode: EXTENDED_SCANCODES.ArrowRight,
      extended: true,
    });
    expect(planKeyDown("Delete", "Delete", NO_MODS)).toEqual({
      kind: "scancode",
      scancode: EXTENDED_SCANCODES.Delete,
      extended: true,
    });
  });

  it("does not flag a non-extended scancode as extended", () => {
    // KeyC without an extended flag must not accidentally claim to be extended.
    expect(planKeyDown("KeyC", "c", { ...NO_MODS, ctrl: true })).not.toHaveProperty("extended", true);
  });
});

describe("scancodeFor", () => {
  it("resolves a plain key to its non-extended scancode", () => {
    expect(scancodeFor("KeyA")).toEqual({ scancode: KEY_SCANCODES.KeyA });
  });

  it("resolves an extended key to its base scancode plus the extended flag", () => {
    expect(scancodeFor("ArrowUp")).toEqual({ scancode: EXTENDED_SCANCODES.ArrowUp, extended: true });
  });

  it("returns undefined for a key with no scancode mapping", () => {
    expect(scancodeFor("Unidentified")).toBeUndefined();
  });
});

describe("formatDuration", () => {
  it("formats elapsed seconds as HH:MM:SS", () => {
    expect(formatDuration(0)).toBe("00:00:00");
    expect(formatDuration(252)).toBe("00:04:12");
    expect(formatDuration(3661)).toBe("01:01:01");
  });
});
