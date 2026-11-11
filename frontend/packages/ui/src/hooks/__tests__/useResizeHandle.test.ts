import { afterEach, describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";
import type React from "react";
import { useResizeHandle } from "../useResizeHandle";

function mouseDownAt(clientX: number) {
  return { preventDefault() {}, clientX, clientY: 0 } as unknown as React.MouseEvent;
}

describe("useResizeHandle", () => {
  afterEach(() => {
    localStorage.clear();
  });

  it("follows a shrinking maxSize and restores the stored size when it grows back", () => {
    localStorage.setItem("panel_width", "1200");
    const { result, rerender } = renderHook(
      ({ maxSize }) => useResizeHandle({ defaultSize: 360, minSize: 280, maxSize, storageKey: "panel_width" }),
      { initialProps: { maxSize: 2000 } }
    );
    expect(result.current.size).toBe(1200);

    rerender({ maxSize: 600 });
    expect(result.current.size).toBe(600);

    rerender({ maxSize: 2000 });
    expect(result.current.size).toBe(1200);
  });

  it("starts a drag from the displayed size after maxSize shrank, not from the stored one", () => {
    localStorage.setItem("panel_width", "1200");
    const { result, rerender } = renderHook(
      ({ maxSize }) =>
        useResizeHandle({ defaultSize: 360, minSize: 280, maxSize, reverse: true, storageKey: "panel_width" }),
      { initialProps: { maxSize: 2000 } }
    );
    rerender({ maxSize: 600 });

    act(() => {
      result.current.handleMouseDown(mouseDownAt(1000));
    });
    act(() => {
      document.dispatchEvent(new MouseEvent("mousemove", { clientX: 1100 }));
    });
    act(() => {
      document.dispatchEvent(new MouseEvent("mouseup"));
    });

    expect(result.current.size).toBe(500);
    expect(localStorage.getItem("panel_width")).toBe("500");
  });
});
