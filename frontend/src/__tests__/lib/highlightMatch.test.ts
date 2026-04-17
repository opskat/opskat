import { describe, it, expect } from "vitest";
import { highlightMatch, filterMatches } from "../../lib/highlightMatch";

describe("highlightMatch", () => {
  it("returns single segment when no query", () => {
    expect(highlightMatch("redis-cache", "")).toEqual([{ text: "redis-cache", match: false }]);
  });

  it("returns single segment when no match", () => {
    expect(highlightMatch("redis-cache", "xyz")).toEqual([{ text: "redis-cache", match: false }]);
  });

  it("highlights single match", () => {
    expect(highlightMatch("redis-cache", "dis")).toEqual([
      { text: "re", match: false },
      { text: "dis", match: true },
      { text: "-cache", match: false },
    ]);
  });

  it("is case-insensitive", () => {
    expect(highlightMatch("Redis-Cache", "red")).toEqual([
      { text: "Red", match: true },
      { text: "is-Cache", match: false },
    ]);
  });

  it("highlights first occurrence only", () => {
    expect(highlightMatch("aaa-aaa", "aaa")).toEqual([
      { text: "aaa", match: true },
      { text: "-aaa", match: false },
    ]);
  });

  it("handles match at start", () => {
    expect(highlightMatch("redis", "red")).toEqual([
      { text: "red", match: true },
      { text: "is", match: false },
    ]);
  });

  it("handles match at end", () => {
    expect(highlightMatch("cache", "che")).toEqual([
      { text: "ca", match: false },
      { text: "che", match: true },
    ]);
  });
});

describe("filterMatches", () => {
  it("filters by substring (case-insensitive)", () => {
    expect(filterMatches("Redis-Cache", "red")).toBe(true);
    expect(filterMatches("Redis-Cache", "XYZ")).toBe(false);
    expect(filterMatches("Redis-Cache", "")).toBe(true);
  });
});
