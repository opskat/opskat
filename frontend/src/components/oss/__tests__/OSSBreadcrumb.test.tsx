import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSBreadcrumb } from "../OSSBreadcrumb";
import { crumbSegments } from "@/lib/ossPrefixTree";

describe("crumbSegments", () => {
  it("returns only the bucket crumb at the root", () => {
    expect(crumbSegments("mb", "")).toEqual([{ label: "mb", prefix: "", isCurrent: true }]);
  });
  it("splits a trailing-slash prefix into cumulative crumbs, last is current", () => {
    expect(crumbSegments("mb", "a/b/")).toEqual([
      { label: "mb", prefix: "", isCurrent: false },
      { label: "a", prefix: "a/", isCurrent: false },
      { label: "b", prefix: "a/b/", isCurrent: true },
    ]);
  });
});

describe("OSSBreadcrumb", () => {
  it("renders each crumb and navigates on click; refresh fires onRefresh", () => {
    const onNavigate = vi.fn();
    const onRefresh = vi.fn();
    render(<OSSBreadcrumb bucket="mb" prefix="a/b/" onNavigate={onNavigate} onRefresh={onRefresh} />);
    expect(screen.getByText("mb")).toBeInTheDocument();
    expect(screen.getByText("a")).toBeInTheDocument();
    expect(screen.getByText("b")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("oss-crumb-1")); // "a"
    expect(onNavigate).toHaveBeenCalledWith("a/");
    fireEvent.click(screen.getByTestId("oss-crumb-0")); // bucket root
    expect(onNavigate).toHaveBeenCalledWith("");
    fireEvent.click(screen.getByTestId("oss-refresh"));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("renders an upload button that fires onUpload when provided", () => {
    const onUpload = vi.fn();
    render(<OSSBreadcrumb bucket="mb" prefix="" onNavigate={vi.fn()} onRefresh={vi.fn()} onUpload={onUpload} />);
    fireEvent.click(screen.getByTestId("oss-upload"));
    expect(onUpload).toHaveBeenCalledTimes(1);
  });

  it("renders a view toggle that fires onViewModeChange", () => {
    const onViewModeChange = vi.fn();
    render(
      <OSSBreadcrumb
        bucket="mb"
        prefix=""
        onNavigate={vi.fn()}
        onRefresh={vi.fn()}
        viewMode="list"
        onViewModeChange={onViewModeChange}
      />
    );
    fireEvent.click(screen.getByTestId("oss-view-grid"));
    expect(onViewModeChange).toHaveBeenCalledWith("grid");
  });

  it("marks path crumbs and view toggles as clearly interactive", () => {
    render(
      <OSSBreadcrumb
        bucket="mb"
        prefix="a/"
        onNavigate={vi.fn()}
        onRefresh={vi.fn()}
        viewMode="list"
        onViewModeChange={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-crumb-0")).toHaveClass("cursor-pointer", "focus-visible:ring-1");
    expect(screen.getByTestId("oss-view-grid")).toHaveClass("cursor-pointer", "focus-visible:ring-1");
  });

  it("exposes the active view and icon-button names", () => {
    render(
      <OSSBreadcrumb
        bucket="mb"
        prefix=""
        onNavigate={vi.fn()}
        onRefresh={vi.fn()}
        viewMode="list"
        onViewModeChange={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-view-list")).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("oss-view-grid")).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByTestId("oss-view-grid")).toHaveAttribute("aria-label", "oss.view.grid");
  });
});
