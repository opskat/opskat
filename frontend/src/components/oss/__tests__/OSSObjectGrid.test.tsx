import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSObjectGrid } from "../OSSObjectGrid";
import type { oss_svc } from "../../../../wailsjs/go/models";

beforeEach(() => {
  vi.stubGlobal(
    "IntersectionObserver",
    class {
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

function obj(key: string, over: Partial<oss_svc.ObjectItem> = {}): oss_svc.ObjectItem {
  return {
    key,
    size: 10,
    lastModified: 0,
    etag: "",
    storageClass: "",
    contentType: "",
    isPrefix: false,
    ...over,
  } as oss_svc.ObjectItem;
}
const base = {
  loading: false,
  loadingPage: false,
  truncated: false,
  focusedKey: null as string | null,
  thumbnails: {},
  onNavigatePrefix: vi.fn(),
  onFocusObject: vi.fn(),
  onEnsureThumbnail: vi.fn(),
  onScrollNearBottom: vi.fn(),
};

describe("OSSObjectGrid", () => {
  it("renders folder and object tiles; single-click a tile focuses it, double-click a folder navigates", () => {
    const onFocusObject = vi.fn(),
      onNavigatePrefix = vi.fn();
    render(
      <OSSObjectGrid
        {...base}
        prefixes={["docs/sub/"]}
        objects={[obj("docs/a.txt")]}
        onFocusObject={onFocusObject}
        onNavigatePrefix={onNavigatePrefix}
      />
    );
    fireEvent.click(screen.getByTestId("oss-grid-object-docs/a.txt"));
    expect(onFocusObject).toHaveBeenCalledWith("docs/a.txt");
    fireEvent.doubleClick(screen.getByTestId("oss-grid-folder-docs/sub/"));
    expect(onNavigatePrefix).toHaveBeenCalledWith("docs/sub/");
  });

  it("shows loading and empty states", () => {
    const { rerender } = render(<OSSObjectGrid {...base} prefixes={[]} objects={[]} loading />);
    expect(screen.getByTestId("oss-grid-loading")).toBeInTheDocument();
    rerender(<OSSObjectGrid {...base} prefixes={[]} objects={[]} />);
    expect(screen.getByTestId("oss-grid-empty")).toBeInTheDocument();
  });

  it("uses keyboard-operable tiles with visible focus feedback", () => {
    const onFocusObject = vi.fn();
    const onNavigatePrefix = vi.fn();
    render(
      <OSSObjectGrid
        {...base}
        prefixes={["docs/sub/"]}
        objects={[obj("docs/a.txt")]}
        onFocusObject={onFocusObject}
        onNavigatePrefix={onNavigatePrefix}
      />
    );
    const folder = screen.getByTestId("oss-grid-folder-docs/sub/");
    const object = screen.getByTestId("oss-grid-object-docs/a.txt");
    expect(folder.tagName).toBe("BUTTON");
    expect(object.tagName).toBe("BUTTON");
    expect(folder).toHaveClass("cursor-pointer", "focus-visible:ring-1");
    fireEvent.keyDown(folder, { key: "Enter" });
    fireEvent.click(object);
    expect(onNavigatePrefix).toHaveBeenCalledWith("docs/sub/");
    expect(onFocusObject).toHaveBeenCalledWith("docs/a.txt");
  });

  it("shows a visible spinner while the initial grid or next page is loading", () => {
    const { rerender } = render(<OSSObjectGrid {...base} prefixes={[]} objects={[]} loading />);
    expect(screen.getByTestId("oss-grid-loading-spinner")).toHaveClass("animate-spin");
    rerender(<OSSObjectGrid {...base} prefixes={[]} objects={[obj("docs/a.txt")]} loadingPage />);
    expect(screen.getByTestId("oss-grid-page-spinner-icon")).toHaveClass("animate-spin");
  });
});
