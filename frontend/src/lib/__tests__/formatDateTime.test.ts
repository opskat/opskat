import { describe, it, expect } from "vitest";
import { formatLocalDateTime } from "../formatDateTime";

describe("formatLocalDateTime", () => {
  it("formats an ISO timestamp as YYYY-MM-DD HH:mm in the local time zone", () => {
    const local = new Date(2026, 10, 7, 9, 5, 30);
    expect(formatLocalDateTime(local.toISOString())).toBe("2026-11-07 09:05");
    expect(formatLocalDateTime(new Date(2026, 0, 31, 23, 59).toISOString())).toBe("2026-01-31 23:59");
  });
});
