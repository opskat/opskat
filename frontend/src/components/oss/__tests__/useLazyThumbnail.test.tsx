import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { useLazyThumbnail } from "../useLazyThumbnail";

let observeCb: IntersectionObserverCallback | null = null;
const observe = vi.fn();
const disconnect = vi.fn();

beforeEach(() => {
  observeCb = null;
  observe.mockClear();
  disconnect.mockClear();
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(cb: IntersectionObserverCallback) {
        observeCb = cb;
      }
      observe = observe;
      disconnect = disconnect;
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as never
  );
});
afterEach(() => vi.unstubAllGlobals());

function Harness({
  enabled,
  resourceKey = "a",
  onEnter,
}: {
  enabled: boolean;
  resourceKey?: string;
  onEnter: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useLazyThumbnail(ref, enabled, resourceKey, onEnter);
  return <div ref={ref} />;
}

describe("useLazyThumbnail", () => {
  it("fires onEnter once when the element intersects, then disconnects", () => {
    const onEnter = vi.fn();
    render(<Harness enabled onEnter={onEnter} />);
    expect(observe).toHaveBeenCalledTimes(1);
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(onEnter).toHaveBeenCalledTimes(1);
    expect(disconnect).toHaveBeenCalledTimes(1);
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(onEnter).toHaveBeenCalledTimes(1); // disconnected after first
  });

  it("does not observe when disabled", () => {
    render(<Harness enabled={false} onEnter={vi.fn()} />);
    expect(observe).not.toHaveBeenCalled();
  });

  it("does not rebuild the observer on re-render with a fresh onEnter closure", () => {
    // Callers (e.g. the object grid) pass a brand-new arrow function every render.
    // The observer effect must not depend on onEnter's identity, otherwise it tears
    // down and recreates the IntersectionObserver on every re-render — re-firing
    // onEnter while the element is still in view (e.g. re-presigning a failed thumbnail).
    const { rerender } = render(<Harness enabled onEnter={() => {}} />);
    rerender(<Harness enabled onEnter={() => {}} />);
    expect(observe).toHaveBeenCalledTimes(1);
  });

  it("rebuilds the observer when the resource changes", () => {
    const { rerender } = render(<Harness enabled resourceKey="a" onEnter={() => {}} />);
    rerender(<Harness enabled resourceKey="b" onEnter={() => {}} />);
    expect(observe).toHaveBeenCalledTimes(2);
  });
});
