import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSBucketTree } from "../OSSBucketTree";
import type { OssPrefixRow } from "@/lib/ossPrefixTree";
import type { oss_svc } from "../../../../wailsjs/go/models";

const buckets: oss_svc.BucketItem[] = [
  { name: "b1", creationDate: 0 } as oss_svc.BucketItem,
  { name: "b2", creationDate: 0 } as oss_svc.BucketItem,
];
const rows: OssPrefixRow[] = [{ depth: 0, name: "docs", prefix: "docs/", isExpanded: false, loaded: false }];
const base = {
  loadingBuckets: false,
  onSelectBucket: vi.fn(),
  onToggleExpand: vi.fn(),
  onNavigatePrefix: vi.fn(),
};

describe("OSSBucketTree", () => {
  it("renders buckets and the selected bucket's prefix rows", () => {
    render(<OSSBucketTree {...base} buckets={buckets} currentBucket="b1" rows={rows} />);
    expect(screen.getByText("b1")).toBeInTheDocument();
    expect(screen.getByText("b2")).toBeInTheDocument();
    expect(screen.getByText("docs")).toBeInTheDocument();
  });

  it("wires select / expand / navigate callbacks", () => {
    const onSelectBucket = vi.fn();
    const onToggleExpand = vi.fn();
    const onNavigatePrefix = vi.fn();
    render(
      <OSSBucketTree
        {...base}
        buckets={buckets}
        currentBucket="b1"
        rows={rows}
        onSelectBucket={onSelectBucket}
        onToggleExpand={onToggleExpand}
        onNavigatePrefix={onNavigatePrefix}
      />
    );
    fireEvent.click(screen.getByTestId("oss-bucket-b2"));
    expect(onSelectBucket).toHaveBeenCalledWith("b2");
    fireEvent.click(screen.getByTestId("oss-tree-toggle-docs/"));
    expect(onToggleExpand).toHaveBeenCalledWith("docs/");
    fireEvent.click(screen.getByTestId("oss-tree-nav-docs/"));
    expect(onNavigatePrefix).toHaveBeenCalledWith("docs/");
  });

  it("shows the no-buckets placeholder for an empty account", () => {
    render(<OSSBucketTree {...base} buckets={[]} currentBucket="" rows={[]} />);
    expect(screen.getByTestId("oss-buckets-empty")).toHaveTextContent("oss.browser.noBuckets");
  });

  it("shows a skeleton while buckets are loading and none are known yet", () => {
    render(<OSSBucketTree {...base} buckets={null} currentBucket="" rows={[]} loadingBuckets />);
    expect(screen.getByTestId("oss-buckets-loading")).toBeInTheDocument();
  });

  it("gives bucket, expand, and path controls pointer and keyboard-focus feedback", () => {
    render(<OSSBucketTree {...base} buckets={buckets} currentBucket="b1" rows={rows} />);
    expect(screen.getByTestId("oss-bucket-b1")).toHaveClass("cursor-pointer", "focus-visible:ring-1");
    expect(screen.getByTestId("oss-tree-toggle-docs/")).toHaveClass("cursor-pointer", "focus-visible:ring-1");
    expect(screen.getByTestId("oss-tree-nav-docs/")).toHaveClass("cursor-pointer", "focus-visible:ring-1");
  });

  it("exposes the selected bucket and folder expansion state", () => {
    render(<OSSBucketTree {...base} buckets={buckets} currentBucket="b1" rows={rows} />);
    expect(screen.getByTestId("oss-bucket-b1")).toHaveAttribute("aria-current", "true");
    expect(screen.getByTestId("oss-tree-toggle-docs/")).toHaveAttribute("aria-expanded", "false");
  });
});
