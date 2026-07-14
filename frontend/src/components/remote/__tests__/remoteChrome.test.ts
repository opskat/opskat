import { describe, expect, it } from "vitest";
import { formatDuration } from "@/components/remote/remoteChrome";

describe("formatDuration", () => {
  it("formats seconds as HH:MM:SS", () => {
    expect(formatDuration(0)).toBe("00:00:00");
    expect(formatDuration(252)).toBe("00:04:12");
    expect(formatDuration(3661)).toBe("01:01:01");
  });
  it("clamps negatives to zero", () => {
    expect(formatDuration(-5)).toBe("00:00:00");
  });
});
