import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SplitPane } from "../components/terminal/SplitPane";
import { useTerminalStore } from "../stores/terminalStore";

describe("SplitPane divider drag", () => {
  const setSplitRatio = vi.fn();

  beforeEach(() => {
    setSplitRatio.mockClear();
    useTerminalStore.setState({ setSplitRatio });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      left: 0,
      top: 0,
      width: 1000,
      height: 500,
      right: 1000,
      bottom: 500,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  function renderSplit() {
    const { container } = render(
      <SplitPane
        node={{
          type: "split",
          direction: "vertical",
          ratio: 0.5,
          first: { type: "pending", pendingId: "p1" },
          second: { type: "pending", pendingId: "p2" },
        }}
        tabId="tab1"
        isTabActive
        activePaneId=""
        showFocusRing={false}
        path={[]}
      />
    );
    const divider = container.querySelector<HTMLElement>(".cursor-col-resize")!;
    const first = divider.previousElementSibling as HTMLElement;
    const second = divider.nextElementSibling as HTMLElement;
    return { divider, first, second };
  }

  it("resizes the DOM while dragging and commits the ratio once on mouseup", () => {
    const { divider, first, second } = renderSplit();

    fireEvent.mouseDown(divider, { clientX: 500 });
    fireEvent.mouseMove(document, { clientX: 300 });
    fireEvent.mouseMove(document, { clientX: 700 });
    expect(setSplitRatio).not.toHaveBeenCalled();
    expect(first.style.flex).toMatch(/^0\.7\b/);
    expect(second.style.flex).toMatch(/^0\.3\b/);

    act(() => {
      fireEvent.mouseUp(document);
    });
    expect(setSplitRatio).toHaveBeenCalledTimes(1);
    expect(setSplitRatio).toHaveBeenCalledWith("tab1", [], 0.7);
  });

  it("clamps the committed ratio to 0.1..0.9", () => {
    const { divider } = renderSplit();

    fireEvent.mouseDown(divider, { clientX: 500 });
    fireEvent.mouseMove(document, { clientX: 5000 });
    fireEvent.mouseUp(document);

    expect(setSplitRatio).toHaveBeenCalledTimes(1);
    expect(setSplitRatio).toHaveBeenCalledWith("tab1", [], 0.9);
  });

  it("does not write a ratio when the divider is released without moving", () => {
    const { divider } = renderSplit();

    fireEvent.mouseDown(divider, { clientX: 500 });
    fireEvent.mouseUp(document);

    expect(setSplitRatio).not.toHaveBeenCalled();
  });
});
