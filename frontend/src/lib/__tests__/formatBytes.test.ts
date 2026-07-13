import { describe, it, expect } from "vitest";
import { formatBytes, formatSpeed } from "../formatBytes";

describe("formatBytes", () => {
  it("formats bytes / KB / MB with one decimal above 1 KB", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1048576)).toBe("1.0 MB");
  });
});

describe("formatSpeed", () => {
  it("appends /s to a byte size", () => {
    expect(formatSpeed(0)).toBe("0 B/s");
    expect(formatSpeed(2048)).toBe("2.0 KB/s");
  });
});
