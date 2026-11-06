import { describe, expect, it } from "vitest";
import { findMatches, revealDelta, type TableFindOptions } from "@/lib/tableFind";
import { CELL_DISPLAY_MAX_CHARS } from "@/lib/cellValue";

const plain: TableFindOptions = { caseSensitive: false, wholeCell: false, regex: false };

const rows: Record<string, unknown>[] = [
  { id: 1, customer: "北岭物流", note: "首单" },
  { id: 2, customer: "青木贸易", note: null },
  { id: 3, customer: "Beiling Logistics", note: "转北岭仓发货" },
];
const columns = ["id", "customer", "note"];

function positions(result: ReturnType<typeof findMatches>) {
  return result.matches.map((m) => `${m.rowIdx}:${m.col}`);
}

describe("findMatches", () => {
  it("returns nothing for an empty query", () => {
    expect(findMatches(rows, columns, undefined, "", plain)).toEqual({ matches: [] });
  });

  it("walks rows top-down and columns in display order, reporting every substring range", () => {
    const result = findMatches(rows, ["note", "customer"], undefined, "北岭", plain);
    expect(positions(result)).toEqual(["0:customer", "2:note"]);
    expect(result.matches[1].ranges).toEqual([[1, 3]]);
  });

  it("only searches the columns it is given (hidden columns stay out)", () => {
    expect(positions(findMatches(rows, ["id", "note"], undefined, "北岭", plain))).toEqual(["2:note"]);
  });

  it("follows the row order the grid is displaying", () => {
    const data = [{ v: "x1" }, { v: "y" }, { v: "x2" }];
    expect(positions(findMatches(data, ["v"], undefined, "x", plain, [2, 1, 0]))).toEqual(["2:v", "0:v"]);
    // rows filtered out of the display are not searched
    expect(positions(findMatches(data, ["v"], undefined, "x", plain, [2, 1]))).toEqual(["2:v"]);
  });

  it("matches every occurrence inside one cell", () => {
    const result = findMatches([{ a: "abcabc" }], ["a"], undefined, "bc", plain);
    expect(result.matches[0].ranges).toEqual([
      [1, 3],
      [4, 6],
    ]);
  });

  it("searches the pending edit instead of the stored value", () => {
    const edits = new Map<string, unknown>([
      ["0:customer", "云栖科技"],
      ["1:note", "北岭加急"],
    ]);
    expect(positions(findMatches(rows, columns, edits, "北岭", plain))).toEqual(["1:note", "2:note"]);
  });

  it("never matches NULL, including the NULL placeholder text and a cell edited to NULL", () => {
    const edits = new Map<string, unknown>([["0:note", null]]);
    expect(findMatches(rows, columns, undefined, "null", plain).matches).toEqual([]);
    expect(positions(findMatches(rows, columns, edits, "首单", plain))).toEqual([]);
  });

  it("matches the displayed text of numbers and objects", () => {
    const data = [{ n: 1280, o: { k: "v" } }];
    expect(positions(findMatches(data, ["n", "o"], undefined, "28", plain))).toEqual(["0:n"]);
    expect(positions(findMatches(data, ["n", "o"], undefined, '"k":"v"', plain))).toEqual(["0:o"]);
  });

  it("matches only within the displayed (truncated) text of a huge value", () => {
    const huge = "a".repeat(CELL_DISPLAY_MAX_CHARS) + "TAIL";
    expect(findMatches([{ v: huge }], ["v"], undefined, "TAIL", plain).matches).toEqual([]);
  });

  it("ignores case by default and respects it when asked", () => {
    expect(positions(findMatches(rows, columns, undefined, "beiling", plain))).toEqual(["2:customer"]);
    expect(findMatches(rows, columns, undefined, "beiling", { ...plain, caseSensitive: true }).matches).toEqual([]);
    expect(positions(findMatches(rows, columns, undefined, "Beiling", { ...plain, caseSensitive: true }))).toEqual([
      "2:customer",
    ]);
  });

  it("treats regex metacharacters literally unless regex mode is on", () => {
    const data = [{ v: "a.c" }, { v: "abc" }];
    expect(positions(findMatches(data, ["v"], undefined, "a.c", plain))).toEqual(["0:v"]);
    expect(positions(findMatches(data, ["v"], undefined, "a.c", { ...plain, regex: true }))).toEqual(["0:v", "1:v"]);
  });

  it("whole-cell mode requires the entire cell text to match", () => {
    const data = [{ v: "paid" }, { v: "unpaid" }, { v: "PAID" }];
    const result = findMatches(data, ["v"], undefined, "paid", { ...plain, wholeCell: true });
    expect(positions(result)).toEqual(["0:v", "2:v"]);
    expect(result.matches[0].ranges).toEqual([[0, 4]]);
    const regexWhole = findMatches(data, ["v"], undefined, "un|pa.d", { ...plain, wholeCell: true, regex: true });
    expect(positions(regexWhole)).toEqual(["0:v", "2:v"]);
  });

  it("reports an invalid regex instead of throwing", () => {
    expect(findMatches(rows, columns, undefined, "(", { ...plain, regex: true })).toEqual({
      matches: [],
      error: "invalidRegex",
    });
  });

  it("skips zero-length regex matches", () => {
    const result = findMatches([{ v: "xay" }], ["v"], undefined, "a*", { ...plain, regex: true });
    expect(result.matches).toEqual([{ rowIdx: 0, col: "v", ranges: [[1, 2]] }]);
    expect(findMatches([{ v: "xy" }], ["v"], undefined, "^", { ...plain, regex: true }).matches).toEqual([]);
  });
});

describe("revealDelta", () => {
  it("does not scroll an item that is already fully visible", () => {
    expect(revealDelta(100, 200, 50, 400)).toBe(0);
  });

  it("scrolls back so an item hidden behind the sticky edge starts right after it", () => {
    // view starts at 250 (scroll edge 50 + 200 px of frozen columns); item sits at 100..260
    expect(revealDelta(100, 260, 250, 900)).toBe(-150);
  });

  it("scrolls forward just enough to show an item past the far edge", () => {
    expect(revealDelta(850, 950, 100, 900)).toBe(50);
  });

  it("keeps the start of an item wider than the view in sight", () => {
    expect(revealDelta(400, 1400, 100, 900)).toBe(300);
  });
});
