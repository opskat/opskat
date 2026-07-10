import { describe, expect, it, vi } from "vitest";
import { pointerCursorStyle } from "../rdpCursor";

function shapePayload(width: number, height: number, byteLength = width * height * 4) {
  return {
    pointerType: "shape",
    hotspotX: 3,
    hotspotY: 5,
    width,
    height,
    data: btoa(String.fromCharCode(...new Uint8Array(byteLength))),
  };
}

describe("pointerCursorStyle", () => {
  it("hides the cursor for a hidden pointer", () => {
    expect(pointerCursorStyle({ pointerType: "hidden" })).toBe("none");
  });

  it("restores the default cursor for a default pointer", () => {
    expect(pointerCursorStyle({ pointerType: "default" })).toBe("default");
  });

  it("renders a shape pointer as a hotspot-anchored image cursor", () => {
    const makeDataURL = vi.fn().mockReturnValue("data:image/png;base64,CURSOR");

    const style = pointerCursorStyle(shapePayload(2, 2), makeDataURL);

    expect(style).toBe("url(data:image/png;base64,CURSOR) 3 5, default");
    const [bytes, width, height] = makeDataURL.mock.calls[0];
    expect([bytes.length, width, height]).toEqual([16, 2, 2]);
  });

  it("falls back to the default cursor when the payload does not match the shape size", () => {
    const makeDataURL = vi.fn();

    expect(pointerCursorStyle(shapePayload(2, 2, 3), makeDataURL)).toBe("default");
    expect(makeDataURL).not.toHaveBeenCalled();
  });

  it("falls back to the default cursor when the runtime cannot rasterize the shape", () => {
    const style = pointerCursorStyle(shapePayload(2, 2), () => null);

    expect(style).toBe("default");
  });
});
