import { describe, expect, it } from "vitest";
import { columnWidths } from "@/components/k8s/cluster/listModel";

const cols = [{ name: "Name" }, { name: "Status" }, { name: "Restarts" }] as never;
const row = (cells: unknown[]) => ({ cells }) as never;

describe("columnWidths", () => {
  it("makes a column at least as wide as its longest value, so no cell needs truncating", () => {
    const narrow = columnWidths(cols, [row(["a", "Running", "0"])]);
    const wide = columnWidths(cols, [row(["a", "Running", "0"]), row(["a", "CrashLoopBackOff", "7 (111s ago)"])]);
    expect(wide[1]).toBeGreaterThan(narrow[1]);
    expect(wide[2]).toBeGreaterThan(narrow[2]);
    expect(wide[0]).toBe(narrow[0]);
  });

  it("is never narrower than the header", () => {
    const [w] = columnWidths([{ name: "A very long header" }] as never, [row(["x"])]);
    expect(w).toBeGreaterThan(columnWidths([{ name: "A" }] as never, [row(["x"])])[0]);
  });
});

describe("columnWidths on large lists", () => {
  it("fits a long value that appears late in a 5000 row list", () => {
    const rows = Array.from({ length: 5000 }, (_, i) => row([`pod-${i}`, "Running", "0"]));
    const short = columnWidths(cols, rows);
    rows[4999] = row(["pod-4999", "CrashLoopBackOff", "7 (111s ago)"]);
    const withLate = columnWidths(cols, rows);
    expect(withLate[1]).toBeGreaterThan(short[1]);
    expect(withLate[2]).toBeGreaterThan(short[2]);
  });
});
