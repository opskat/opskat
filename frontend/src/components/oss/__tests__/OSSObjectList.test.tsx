import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { OSSObjectList } from "../OSSObjectList";
import { formatBytes } from "@/lib/formatBytes";
import { shouldLoadNextPage } from "@/lib/ossListScroll";
import type { oss_svc } from "../../../../wailsjs/go/models";

const { toastSuccess, toastError } = vi.hoisted(() => ({ toastSuccess: vi.fn(), toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));

function obj(key: string, size: number): oss_svc.ObjectItem {
  return {
    key,
    size,
    lastModified: 1751811127,
    etag: "",
    storageClass: "STANDARD",
    contentType: "",
    isPrefix: false,
  } as oss_svc.ObjectItem;
}

describe("formatBytes", () => {
  it("formats bytes / KB / MB", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1048576)).toBe("1.0 MB");
  });
});

describe("shouldLoadNextPage", () => {
  it("is false when not truncated or already loading a page", () => {
    expect(shouldLoadNextPage(900, 100, 1000, false, false)).toBe(false);
    expect(shouldLoadNextPage(900, 100, 1000, true, true)).toBe(false);
  });
  it("is true only near the bottom of a truncated list", () => {
    expect(shouldLoadNextPage(900, 100, 1000, true, false)).toBe(true);
    expect(shouldLoadNextPage(100, 100, 1000, true, false)).toBe(false);
  });
});

describe("OSSObjectList", () => {
  const base = {
    selection: new Set<string>(),
    loading: false,
    loadingPage: false,
    truncated: false,
    onNavigatePrefix: vi.fn(),
    onToggleSelect: vi.fn(),
    onScrollNearBottom: vi.fn(),
  };

  it("renders folder rows and object rows with leaf names + size", () => {
    render(<OSSObjectList {...base} prefixes={["docs/sub/"]} objects={[obj("docs/readme.txt", 1536)]} />);
    expect(screen.getByText("sub")).toBeInTheDocument();
    expect(screen.getByText("readme.txt")).toBeInTheDocument();
    expect(screen.getByText("1.5 KB")).toBeInTheDocument();
    // storage class renders as a chip, not bare text
    expect(screen.getByText("STANDARD").className).toContain("bg-muted");
  });

  it("double-clicking a folder navigates into it", () => {
    const onNavigatePrefix = vi.fn();
    render(<OSSObjectList {...base} onNavigatePrefix={onNavigatePrefix} prefixes={["docs/sub/"]} objects={[]} />);
    fireEvent.doubleClick(screen.getByTestId("oss-folder-docs/sub/"));
    expect(onNavigatePrefix).toHaveBeenCalledWith("docs/sub/");
  });

  it("does not show a delete action on folder rows", () => {
    render(<OSSObjectList {...base} prefixes={["docs/sub/"]} objects={[]} />);
    expect(within(screen.getByTestId("oss-folder-docs/sub/")).queryByRole("button")).not.toBeInTheDocument();
  });

  it("clicking an object checkbox toggles its selection", () => {
    const onToggleSelect = vi.fn();
    render(<OSSObjectList {...base} onToggleSelect={onToggleSelect} prefixes={[]} objects={[obj("docs/a.txt", 1)]} />);
    fireEvent.click(screen.getByTestId("oss-select-docs/a.txt"));
    expect(onToggleSelect).toHaveBeenCalledWith("docs/a.txt");
  });

  it("shows an empty-folder placeholder when there are no prefixes and no objects", () => {
    render(<OSSObjectList {...base} prefixes={[]} objects={[]} />);
    expect(screen.getByTestId("oss-list-empty")).toHaveTextContent("oss.browser.emptyDir");
  });

  it("shows a download button on object rows that fires onDownload", () => {
    const onDownload = vi.fn();
    render(
      <OSSObjectList {...base} onDownload={onDownload} prefixes={["docs/sub/"]} objects={[obj("docs/a.txt", 1)]} />
    );
    fireEvent.click(screen.getByTestId("oss-download-docs/a.txt"));
    expect(onDownload).toHaveBeenCalledWith("docs/a.txt");
    // folders get no download button
    expect(screen.queryByTestId("oss-download-docs/sub/")).toBeNull();
  });

  it("single-click an object row focuses it; the focused row is marked; clicking the checkbox does NOT focus", () => {
    const onFocusObject = vi.fn();
    const onToggleSelect = vi.fn();
    render(
      <OSSObjectList
        {...base}
        onToggleSelect={onToggleSelect}
        onFocusObject={onFocusObject}
        focusedKey="docs/a.txt"
        prefixes={[]}
        objects={[obj("docs/a.txt", 1)]}
      />
    );
    const row = screen.getByTestId("oss-object-docs/a.txt");
    expect(row.className.split(/\s+/)).toContain("bg-accent");
    fireEvent.click(row);
    expect(onFocusObject).toHaveBeenCalledWith("docs/a.txt");
    onFocusObject.mockClear();
    fireEvent.click(screen.getByTestId("oss-select-docs/a.txt")); // checkbox → select, NOT focus
    expect(onToggleSelect).toHaveBeenCalledWith("docs/a.txt");
    expect(onFocusObject).not.toHaveBeenCalled();
  });

  it("does not highlight a row whose key is not the focusedKey", () => {
    render(
      <OSSObjectList
        {...base}
        focusedKey="docs/a.txt"
        prefixes={[]}
        objects={[obj("docs/a.txt", 1), obj("docs/b.txt", 2)]}
      />
    );
    const focusedRow = screen.getByTestId("oss-object-docs/a.txt");
    const otherRow = screen.getByTestId("oss-object-docs/b.txt");
    expect(focusedRow.className.split(/\s+/)).toContain("bg-accent");
    expect(otherRow.className.split(/\s+/)).not.toContain("bg-accent");
  });

  it("supports keyboard navigation and focus for interactive rows", () => {
    const onNavigatePrefix = vi.fn();
    const onFocusObject = vi.fn();
    render(
      <OSSObjectList
        {...base}
        onNavigatePrefix={onNavigatePrefix}
        onFocusObject={onFocusObject}
        prefixes={["docs/sub/"]}
        objects={[obj("docs/a.txt", 1)]}
      />
    );
    const folder = screen.getByTestId("oss-folder-docs/sub/");
    const object = screen.getByTestId("oss-object-docs/a.txt");
    expect(folder).toHaveAttribute("tabindex", "0");
    expect(object).toHaveAttribute("tabindex", "0");
    fireEvent.keyDown(folder, { key: "Enter" });
    fireEvent.keyDown(object, { key: "Enter" });
    expect(onNavigatePrefix).toHaveBeenCalledWith("docs/sub/");
    expect(onFocusObject).toHaveBeenCalledWith("docs/a.txt");
  });

  it("marks the row download action as clearly interactive", () => {
    render(<OSSObjectList {...base} onDownload={vi.fn()} prefixes={[]} objects={[obj("docs/a.txt", 1)]} />);
    expect(screen.getByTestId("oss-download-docs/a.txt")).toHaveClass("cursor-pointer", "focus-visible:ring-1");
  });

  it("shows a visible spinner while the initial list or next page is loading", () => {
    const { rerender } = render(<OSSObjectList {...base} prefixes={[]} objects={[]} loading />);
    expect(screen.getByTestId("oss-list-loading-spinner")).toHaveClass("animate-spin");
    rerender(<OSSObjectList {...base} prefixes={[]} objects={[obj("docs/a.txt", 1)]} loadingPage />);
    expect(screen.getByTestId("oss-list-page-spinner-icon")).toHaveClass("animate-spin");
  });

  it("loads the next page only when a truncated list scrolls near the bottom", () => {
    const onScrollNearBottom = vi.fn();
    render(
      <OSSObjectList
        {...base}
        prefixes={[]}
        objects={[obj("docs/a.txt", 1)]}
        truncated
        onScrollNearBottom={onScrollNearBottom}
      />
    );
    const list = screen.getByTestId("oss-object-list");
    Object.defineProperties(list, {
      clientHeight: { configurable: true, value: 100 },
      scrollHeight: { configurable: true, value: 1000 },
    });

    fireEvent.scroll(list, { target: { scrollTop: 100 } });
    expect(onScrollNearBottom).not.toHaveBeenCalled();
    fireEvent.scroll(list, { target: { scrollTop: 900 } });
    expect(onScrollNearBottom).toHaveBeenCalledOnce();
  });
});

describe("OSSObjectList — keyboard copy", () => {
  const writeText = vi.fn();

  beforeEach(() => {
    writeText.mockReset();
    writeText.mockResolvedValue(undefined);
    toastSuccess.mockReset();
    toastError.mockReset();
    window.getSelection()?.removeAllRanges();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
  });

  const base = {
    selection: new Set<string>(),
    loading: false,
    loadingPage: false,
    truncated: false,
    onNavigatePrefix: vi.fn(),
    onToggleSelect: vi.fn(),
    onScrollNearBottom: vi.fn(),
  };
  const copyKey = { key: "c", code: "KeyC", ctrlKey: true, metaKey: true };
  const objects = [obj("docs/a.txt", 1), obj("docs/b.txt", 2), obj("docs/c.txt", 3)];

  it("copies the selected object keys one per line", async () => {
    render(
      <OSSObjectList {...base} selection={new Set(["docs/a.txt", "docs/c.txt"])} prefixes={[]} objects={objects} />
    );

    fireEvent.keyDown(screen.getByTestId("oss-object-docs/a.txt"), copyKey);

    expect(writeText).toHaveBeenCalledWith("docs/a.txt\ndocs/c.txt");
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("oss.keyCopied", expect.anything()));
  });

  it("copies the focused object when nothing is selected", () => {
    render(<OSSObjectList {...base} focusedKey="docs/b.txt" prefixes={[]} objects={objects} />);

    fireEvent.keyDown(screen.getByTestId("oss-object-docs/b.txt"), copyKey);

    expect(writeText).toHaveBeenCalledWith("docs/b.txt");
  });

  it("leaves Ctrl/Cmd+C to the browser while object text is selected", () => {
    render(<OSSObjectList {...base} focusedKey="docs/b.txt" prefixes={[]} objects={objects} />);
    const label = screen.getByText("b.txt");
    const range = document.createRange();
    range.selectNodeContents(label);
    window.getSelection()!.addRange(range);
    const event = new KeyboardEvent("keydown", { ...copyKey, bubbles: true, cancelable: true });

    screen.getByTestId("oss-object-docs/b.txt").dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
    expect(writeText).not.toHaveBeenCalled();
  });

  it("copies the checked objects from a folder row too", () => {
    render(<OSSObjectList {...base} selection={new Set(["docs/a.txt"])} prefixes={["docs/sub/"]} objects={objects} />);

    fireEvent.keyDown(screen.getByTestId("oss-folder-docs/sub/"), copyKey);

    expect(writeText).toHaveBeenCalledWith("docs/a.txt");
  });

  it("leaves Ctrl/Cmd+C alone on a folder row when nothing is checked", () => {
    render(<OSSObjectList {...base} prefixes={["docs/sub/"]} objects={objects} />);

    const ev = new KeyboardEvent("keydown", { ...copyKey, bubbles: true, cancelable: true });
    screen.getByTestId("oss-folder-docs/sub/").dispatchEvent(ev);

    expect(ev.defaultPrevented).toBe(false);
    expect(writeText).not.toHaveBeenCalled();
  });
});
