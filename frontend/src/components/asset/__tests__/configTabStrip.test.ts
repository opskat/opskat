import { describe, it, expect } from "vitest";
import { STRIP_FADE, revealScrollLeft, stripEdges } from "@/components/asset/configTabStrip";

describe("stripEdges", () => {
  it("reports no hidden side when every tab fits", () => {
    expect(stripEdges({ scrollLeft: 0, clientWidth: 500, scrollWidth: 500 })).toEqual({ left: false, right: false });
  });

  it("reports the side tabs have scrolled out of", () => {
    expect(stripEdges({ scrollLeft: 0, clientWidth: 500, scrollWidth: 620 })).toEqual({ left: false, right: true });
    expect(stripEdges({ scrollLeft: 60, clientWidth: 500, scrollWidth: 620 })).toEqual({ left: true, right: true });
    expect(stripEdges({ scrollLeft: 120, clientWidth: 500, scrollWidth: 620 })).toEqual({ left: true, right: false });
  });

  it("ignores sub-pixel rounding at either end", () => {
    expect(stripEdges({ scrollLeft: 0.5, clientWidth: 500, scrollWidth: 620.4 })).toEqual({ left: false, right: true });
    expect(stripEdges({ scrollLeft: 119.6, clientWidth: 500, scrollWidth: 620 })).toEqual({ left: true, right: false });
  });
});

describe("revealScrollLeft", () => {
  const strip = { scrollLeft: 60, clientWidth: 500, scrollWidth: 800 };

  it("leaves the strip alone when the tab is already fully visible", () => {
    expect(revealScrollLeft(strip, { left: 200, width: 80 })).toBe(60);
  });

  it("scrolls right just far enough to show a tab cut off at the right edge, clear of the fade", () => {
    // tab spans 520..620; the visible window is 60..560
    expect(revealScrollLeft(strip, { left: 520, width: 100 })).toBe(620 - 500 + STRIP_FADE);
  });

  it("scrolls left just far enough to show a tab cut off at the left edge, clear of the fade", () => {
    expect(revealScrollLeft(strip, { left: 40, width: 60 })).toBe(Math.max(0, 40 - STRIP_FADE));
  });

  it("treats a tab sitting under an edge fade as cut off", () => {
    // fully inside the window (60..560) but reaching into the fade on its right
    expect(revealScrollLeft(strip, { left: 470, width: 80 })).toBe(550 - 500 + STRIP_FADE);
  });

  it("never scrolls past either end of the strip", () => {
    expect(revealScrollLeft(strip, { left: 0, width: 50 })).toBe(0);
    expect(revealScrollLeft(strip, { left: 720, width: 80 })).toBe(300);
  });
});
