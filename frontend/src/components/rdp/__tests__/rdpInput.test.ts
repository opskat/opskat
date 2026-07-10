import { describe, it, expect } from "vitest";
import { clampRemoteSize, chordSequence, formatDuration, SCANCODE, MIN_REMOTE_WIDTH, MIN_REMOTE_HEIGHT } from "../rdpInput";

describe("clampRemoteSize", () => {
  it("rounds fractional viewport dimensions to whole pixels", () => {
    expect(clampRemoteSize(1600.4, 900.6)).toEqual({ width: 1600, height: 901 });
  });

  it("clamps dimensions below the RDP minimum up to the minimum", () => {
    expect(clampRemoteSize(300, 200)).toEqual({ width: MIN_REMOTE_WIDTH, height: MIN_REMOTE_HEIGHT });
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

describe("formatDuration", () => {
  it("formats elapsed seconds as HH:MM:SS", () => {
    expect(formatDuration(0)).toBe("00:00:00");
    expect(formatDuration(252)).toBe("00:04:12");
    expect(formatDuration(3661)).toBe("01:01:01");
  });
});
