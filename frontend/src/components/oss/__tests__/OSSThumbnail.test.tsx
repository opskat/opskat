import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSThumbnail } from "../OSSThumbnail";

let observeCb: IntersectionObserverCallback | null = null;
beforeEach(() => {
  observeCb = null;
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      constructor(cb: IntersectionObserverCallback) {
        observeCb = cb;
      }
      observe = vi.fn();
      disconnect = vi.fn();
      unobserve = vi.fn();
      takeRecords = vi.fn();
      root = null;
      rootMargin = "";
      thresholds = [];
    } as never
  );
});
afterEach(() => vi.unstubAllGlobals());

describe("OSSThumbnail", () => {
  it("calls onEnsure when an image scrolls into view and renders the img once url is set", () => {
    const onEnsure = vi.fn();
    const { rerender } = render(<OSSThumbnail objectKey="a.png" contentType="image/png" onEnsure={onEnsure} />);
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(onEnsure).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("oss-thumb-img")).toBeNull(); // no url yet → placeholder
    rerender(<OSSThumbnail objectKey="a.png" contentType="image/png" url="https://x/a" onEnsure={onEnsure} />);
    expect(screen.getByTestId("oss-thumb-img")).toHaveAttribute("src", "https://x/a");
  });

  it("starts lazy loading again when the rendered image object changes", () => {
    const ensureA = vi.fn();
    const ensureB = vi.fn();
    const { rerender } = render(<OSSThumbnail objectKey="a.png" contentType="image/png" onEnsure={ensureA} />);

    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
    expect(ensureA).toHaveBeenCalledTimes(1);

    rerender(<OSSThumbnail objectKey="b.png" contentType="image/png" onEnsure={ensureB} />);
    observeCb!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);

    expect(ensureB).toHaveBeenCalledTimes(1);
  });

  it("renders a type icon (no img, no ensure) for a non-image", () => {
    const onEnsure = vi.fn();
    render(<OSSThumbnail objectKey="a.json" contentType="application/json" onEnsure={onEnsure} />);
    expect(screen.queryByTestId("oss-thumb-img")).toBeNull();
    expect(screen.getByTestId("oss-thumb-icon")).toBeInTheDocument();
    expect(onEnsure).not.toHaveBeenCalled();
  });

  it("falls back to the icon when the image errors", () => {
    render(<OSSThumbnail objectKey="a.png" contentType="image/png" url="https://x/broken" onEnsure={vi.fn()} />);
    fireEvent.error(screen.getByTestId("oss-thumb-img"));
    expect(screen.getByTestId("oss-thumb-icon")).toBeInTheDocument();
  });

  it("does not carry an image error into the next object", () => {
    const { rerender } = render(
      <OSSThumbnail objectKey="a.png" contentType="image/png" url="https://x/a" onEnsure={vi.fn()} />
    );
    fireEvent.error(screen.getByTestId("oss-thumb-img"));

    rerender(<OSSThumbnail objectKey="b.png" contentType="image/png" url="https://x/b" onEnsure={vi.fn()} />);

    expect(screen.getByTestId("oss-thumb-img")).toHaveAttribute("src", "https://x/b");
  });
});
