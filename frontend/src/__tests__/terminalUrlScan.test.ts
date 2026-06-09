import { describe, it, expect } from "vitest";
import { findUrlRowSpans, normalizeHttpUrl } from "@/components/terminal/terminalUrlScan";

type Cell = { chars: string; width: number };

function makeLine(cells: Cell[], isWrapped = false) {
  return {
    isWrapped,
    length: cells.length,
    getCell: (x: number) => {
      const c = cells[x];
      if (!c) return undefined;
      return { getWidth: () => c.width, getChars: () => c.chars };
    },
  };
}

function asciiLine(s: string, isWrapped = false) {
  return makeLine(
    [...s].map((ch) => ({ chars: ch, width: 1 })),
    isWrapped
  );
}

function wide(ch: string): Cell[] {
  // A width-2 glyph occupies two cells: the glyph cell + a width-0 spacer.
  return [
    { chars: ch, width: 2 },
    { chars: "", width: 0 },
  ];
}

function makeBuffer(lines: ReturnType<typeof makeLine>[]) {
  return { length: lines.length, getLine: (y: number) => lines[y] };
}

describe("normalizeHttpUrl", () => {
  it("accepts http/https and rejects others", () => {
    expect(normalizeHttpUrl("http://a.com")).toBe("http://a.com");
    expect(normalizeHttpUrl("https://a.com/x")).toBe("https://a.com/x");
    expect(normalizeHttpUrl("ftp://a.com")).toBeUndefined();
    expect(normalizeHttpUrl("not a url")).toBeUndefined();
  });
});

describe("findUrlRowSpans", () => {
  it("finds a single URL with correct columns on an ascii line", () => {
    const buf = makeBuffer([asciiLine("see http://a.com x")]);
    const spans = findUrlRowSpans(buf, 0, 0, 80);
    expect(spans).toEqual([{ line: 0, startCol: 4, width: 12, url: "http://a.com" }]);
  });

  it("trims trailing punctuation so highlight matches click span", () => {
    const buf = makeBuffer([asciiLine("visit http://a.com.")]);
    const spans = findUrlRowSpans(buf, 0, 0, 80);
    expect(spans).toEqual([{ line: 0, startCol: 6, width: 12, url: "http://a.com" }]);
  });

  it("ignores non-http tokens", () => {
    const buf = makeBuffer([asciiLine("ftp://a.com and plain text")]);
    expect(findUrlRowSpans(buf, 0, 0, 80)).toEqual([]);
  });

  it("computes real columns when a wide CJK char precedes the URL", () => {
    // "你" is 2 columns; the URL must start at column 2, not string index 1.
    const buf = makeBuffer([makeLine([...wide("你"), ...[..."http://a.com"].map((c) => ({ chars: c, width: 1 }))])]);
    const spans = findUrlRowSpans(buf, 0, 0, 80);
    expect(spans).toEqual([{ line: 0, startCol: 2, width: 12, url: "http://a.com" }]);
  });

  it("emits per-row segments for a URL wrapped across two rows", () => {
    const buf = makeBuffer([asciiLine("see http://exa"), asciiLine("mple.com/x", true)]);
    const spans = findUrlRowSpans(buf, 0, 1, 80);
    expect(spans).toEqual([
      { line: 0, startCol: 4, width: 10, url: "http://example.com/x" },
      { line: 1, startCol: 0, width: 10, url: "http://example.com/x" },
    ]);
  });

  it("walks up to the logical line start when the window opens on a wrapped row", () => {
    const buf = makeBuffer([asciiLine("see http://exa"), asciiLine("mple.com/x", true)]);
    const spans = findUrlRowSpans(buf, 1, 1, 80);
    expect(spans).toEqual([
      { line: 0, startCol: 4, width: 10, url: "http://example.com/x" },
      { line: 1, startCol: 0, width: 10, url: "http://example.com/x" },
    ]);
  });

  it("does not hang when the buffer reports rows that getLine cannot return", () => {
    // buffer.length claims 3 rows but rows 1-2 are missing (e.g. trimmed concurrently).
    const present = asciiLine("see http://a.com");
    const buf = { length: 3, getLine: (y: number) => (y === 0 ? present : undefined) };
    const spans = findUrlRowSpans(buf, 0, 2, 80);
    expect(spans).toEqual([{ line: 0, startCol: 4, width: 12, url: "http://a.com" }]);
  });
});
