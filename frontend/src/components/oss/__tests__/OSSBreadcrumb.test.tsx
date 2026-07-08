import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSBreadcrumb, crumbSegments } from "../OSSBreadcrumb";

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
});
