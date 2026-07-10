import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSObjectDetail } from "../OSSObjectDetail";
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

function obj(over: Partial<oss_svc.ObjectItem> = {}): oss_svc.ObjectItem {
  return {
    key: "docs/report.pdf",
    size: 2048,
    lastModified: 0,
    etag: "e1",
    storageClass: "STANDARD",
    contentType: "application/pdf",
    isPrefix: false,
    ...over,
  } as oss_svc.ObjectItem;
}

describe("OSSObjectDetail", () => {
  it("renders metadata from the object and fires action callbacks", () => {
    const onShare = vi.fn(),
      onDownload = vi.fn(),
      onCopy = vi.fn(),
      onMove = vi.fn(),
      onRename = vi.fn(),
      onDelete = vi.fn(),
      onClose = vi.fn();
    render(
      <OSSObjectDetail
        object={obj()}
        onEnsureThumbnail={vi.fn()}
        onShare={onShare}
        onDownload={onDownload}
        onCopy={onCopy}
        onMove={onMove}
        onRename={onRename}
        onDelete={onDelete}
        onClose={onClose}
      />
    );
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("STANDARD")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("oss-detail-share"));
    fireEvent.click(screen.getByTestId("oss-detail-download"));
    fireEvent.click(screen.getByTestId("oss-detail-copy"));
    fireEvent.click(screen.getByTestId("oss-detail-move"));
    fireEvent.click(screen.getByTestId("oss-detail-rename"));
    fireEvent.click(screen.getByTestId("oss-detail-delete"));
    fireEvent.click(screen.getByTestId("oss-detail-close"));
    expect(onShare).toHaveBeenCalledTimes(1);
    expect(onDownload).toHaveBeenCalledTimes(1);
    expect(onCopy).toHaveBeenCalledTimes(1);
    expect(onMove).toHaveBeenCalledTimes(1);
    expect(onRename).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("never exposes a direct copy-presigned-url action", () => {
    render(
      <OSSObjectDetail
        object={obj()}
        onEnsureThumbnail={vi.fn()}
        onShare={vi.fn()}
        onDownload={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.queryByTestId("oss-detail-copy-url")).toBeNull();
  });

  it("shows an icon thumbnail for a non-image and an img for an image with a url", () => {
    const { rerender } = render(
      <OSSObjectDetail
        object={obj()}
        onEnsureThumbnail={vi.fn()}
        onShare={vi.fn()}
        onDownload={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-thumb-icon")).toBeInTheDocument();
    rerender(
      <OSSObjectDetail
        object={obj({ key: "img/a.png", contentType: "image/png" })}
        thumbnailUrl="https://x/a"
        onEnsureThumbnail={vi.fn()}
        onShare={vi.fn()}
        onDownload={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-thumb-img")).toHaveAttribute("src", "https://x/a");
  });
});
