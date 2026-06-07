import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatLoad,
  formatPercent,
  formatUptime,
  getHealthLevel,
  usagePercent,
} from "./serverStatusMetrics";

describe("serverStatusMetrics", () => {
  it("formats values defensively", () => {
    expect(formatPercent(5.3)).toBe("5.3%");
    expect(formatPercent(null)).toBe("-");
    expect(formatBytes(undefined)).toBe("-");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatLoad(0)).toBe("-");
    expect(formatLoad(0.08)).toBe("0.08");
    expect(usagePercent(512, 1024)).toBe(50);
    expect(usagePercent(1, 0)).toBeNull();
  });

  it("does NOT flag a normal 60% disk as warning (the #157 bug)", () => {
    expect(getHealthLevel(5, 45, 60)).toBe("healthy");
  });

  it("uses per-metric thresholds, worst wins", () => {
    expect(getHealthLevel(75, 45, 32)).toBe("warning"); // cpu>=70
    expect(getHealthLevel(95, 45, 32)).toBe("critical"); // cpu>=90
    expect(getHealthLevel(10, 10, 85)).toBe("warning"); // disk>=80
    expect(getHealthLevel(10, 10, 95)).toBe("critical"); // disk>=92
  });

  it("returns unknown when no metric is available", () => {
    expect(getHealthLevel(null, null, null)).toBe("unknown");
  });

  it("formats raw uptime output for compact display", () => {
    expect(formatUptime("00:44:57 up 108 days, 23:19, 14 users, load average: 2.33, 1.89, 1.64", "zh-CN")).toBe(
      "108天 23小时 19分"
    );
    expect(formatUptime("up 4:05, 1 user, load average: 0.01, 0.02, 0.03", "zh-CN")).toBe("4小时 5分");
    expect(formatUptime("up 12 min, 1 user, load average: 0.01, 0.02, 0.03", "zh-CN")).toBe("12分");
    expect(formatUptime("up 2 days, 1:03, 1 user, load average: 0.01, 0.02, 0.03", "en")).toBe("2d 1h 3m");
  });
});
