import { describe, expect, it } from "vitest";
import { extractOlderLogLines } from "./k8sLogPagination";

describe("extractOlderLogLines", () => {
  it("returns the lines older than the previous tail window", () => {
    const snapshot = "line-1\nline-2\nline-3\nline-4\n";
    const { older, reachedStart } = extractOlderLogLines(snapshot, 2, 4);
    expect(older).toBe("line-1\nline-2\n");
    expect(reachedStart).toBe(false);
  });

  it("marks reached start when the snapshot is shorter than requested", () => {
    const snapshot = "only\n";
    const { older, reachedStart } = extractOlderLogLines(snapshot, 200, 400);
    expect(older).toBe("");
    expect(reachedStart).toBe(true);
  });
});
