import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import {
  OSSCopyObject,
  OSSCreateFolder,
  OSSDownloadFolder,
  OSSMoveObject,
  OSSListBuckets,
  OSSListObjects,
  OSSPickUploadFiles,
  OSSRemoveObject,
  OSSUploadObjectPath,
} from "../../../../wailsjs/go/oss/OSS";
import { StatLocalPaths } from "../../../../wailsjs/go/system/System";
import { OSSBrowserPanel } from "../OSSBrowserPanel";
import { useTabStore } from "@/stores/tabStore";
import { useOssBrowserStore } from "@/stores/ossBrowserStore";
import { useAssetStore } from "@/stores/assetStore";
import { toast } from "sonner";

const TAB = "query-7";

beforeEach(() => {
  vi.mocked(OSSListBuckets).mockReset();
  vi.mocked(OSSListObjects).mockReset();
  vi.mocked(OSSDownloadFolder).mockReset();
  useOssBrowserStore.setState({ tabs: {} });
  useAssetStore.setState({ assets: [] });
  useTabStore.setState({
    tabs: [
      {
        id: TAB,
        type: "query",
        label: "b",
        meta: { type: "query", assetId: 7, assetName: "b", assetIcon: "", assetType: "oss" },
      },
    ],
    activeTabId: TAB,
  });
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

describe("OSSBrowserPanel", () => {
  it("shows the bucket-list failure in the sidebar and retries it", async () => {
    vi.mocked(OSSListBuckets).mockRejectedValueOnce(new Error("AccessDenied"));
    render(<OSSBrowserPanel tabId={TAB} />);
    const sidebar = await screen.findByTestId("oss-sidebar");
    expect(await within(sidebar).findByRole("alert")).toHaveTextContent("AccessDenied");

    vi.mocked(OSSListBuckets).mockResolvedValueOnce([{ name: "b1", creationDate: 0 }] as never);
    fireEvent.click(within(sidebar).getByTestId("async-state-retry"));
    expect(await within(sidebar).findByTestId("oss-bucket-b1")).toBeInTheDocument();
    expect(OSSListBuckets).toHaveBeenCalledTimes(2);
  });

  it("keeps the loaded bucket list when a later object operation fails", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValueOnce([{ name: "b1", creationDate: 0 }] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    const sidebar = await screen.findByTestId("oss-sidebar");
    expect(await within(sidebar).findByTestId("oss-bucket-b1")).toBeInTheDocument();

    vi.mocked(OSSRemoveObject).mockRejectedValueOnce(new Error("AccessDenied on delete"));
    await act(async () => {
      await useOssBrowserStore
        .getState()
        .deleteObject(TAB, "a.txt")
        .catch(() => undefined);
    });

    expect(within(sidebar).getByTestId("oss-bucket-b1")).toBeInTheDocument();
    expect(within(sidebar).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("loads buckets on mount and renders them in the tree", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await waitFor(() => expect(OSSListBuckets).toHaveBeenCalledWith(7));
    expect(await screen.findByTestId("oss-bucket-b1")).toBeInTheDocument();
    expect(screen.getByTestId("oss-browser-panel")).toBeInTheDocument();
  });

  it("opens straight into the asset's default bucket", async () => {
    useAssetStore.setState({
      assets: [{ ID: 7, Type: "oss", Config: '{"endpoint":"e","access_key_id":"a","bucket":"ootd"}' }] as never,
    });
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "ootd", creationDate: 0 }] as never);
    vi.mocked(OSSListObjects).mockResolvedValue({
      prefixes: [],
      objects: [],
      nextContinuationToken: "",
      isTruncated: false,
    } as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await waitFor(() =>
      expect(OSSListObjects).toHaveBeenCalledWith(expect.objectContaining({ assetId: 7, bucket: "ootd", prefix: "" }))
    );
    expect(screen.queryByTestId("oss-no-bucket")).not.toBeInTheDocument();
  });

  it.each(["list", "grid"] as const)(
    "shows why the object listing failed in the %s view, not an empty folder, and retries it",
    async (viewMode) => {
      useAssetStore.setState({
        assets: [{ ID: 7, Type: "oss", Config: '{"endpoint":"e","access_key_id":"a","bucket":"ootd"}' }] as never,
      });
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "ootd", creationDate: 0 }] as never);
      vi.mocked(OSSListObjects)
        .mockRejectedValueOnce(new Error("AccessDenied: list not allowed"))
        .mockResolvedValueOnce({
          prefixes: [],
          objects: [
            { key: "a.txt", size: 1, lastModified: 0, etag: "", storageClass: "", contentType: "", isPrefix: false },
          ],
          nextContinuationToken: "",
          isTruncated: false,
        } as never);
      useOssBrowserStore.setState({
        tabs: {
          [TAB]: {
            assetId: 7,
            buckets: null,
            currentBucket: "",
            currentPrefix: "",
            tree: {},
            expanded: new Set(),
            listing: null,
            selection: new Set(),
            loading: { buckets: false, listing: false, page: false },
            bucketsError: null,
            listingError: null,
            viewMode,
            focusedKey: null,
            thumbnails: {},
          },
        },
      });

      render(<OSSBrowserPanel tabId={TAB} />);

      expect(await screen.findByRole("alert")).toHaveTextContent("AccessDenied: list not allowed");
      expect(screen.queryByText("oss.browser.emptyDir")).not.toBeInTheDocument();

      fireEvent.click(screen.getByRole("button", { name: "action.retry" }));

      expect(await screen.findByText("a.txt")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(OSSListObjects).toHaveBeenCalledTimes(2);
    }
  );

  it("reports a failed listing retry inline only, not again as a toast", async () => {
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => "");
    useAssetStore.setState({
      assets: [{ ID: 7, Type: "oss", Config: '{"endpoint":"e","access_key_id":"a","bucket":"ootd"}' }] as never,
    });
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "ootd", creationDate: 0 }] as never);
    vi.mocked(OSSListObjects)
      .mockRejectedValueOnce(new Error("AccessDenied"))
      .mockRejectedValueOnce(new Error("SlowDown"));
    render(<OSSBrowserPanel tabId={TAB} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("AccessDenied");

    fireEvent.click(screen.getByRole("button", { name: "action.retry" }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("SlowDown"));
    expect(toastError).not.toHaveBeenCalled();
    toastError.mockRestore();
  });

  it("guards against a missing asset id", () => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    render(<OSSBrowserPanel tabId="nope" />);
    expect(screen.getByText("oss.browser.missingAsset")).toBeInTheDocument();
  });

  it("clicking upload picks local files and uploads them into the current prefix", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    vi.mocked(OSSListObjects)
      .mockReset()
      .mockResolvedValue({
        prefixes: [],
        objects: [],
        nextContinuationToken: "",
        isTruncated: false,
      } as never);
    vi.mocked(OSSPickUploadFiles).mockResolvedValue(["/local/a.txt"] as never);
    vi.mocked(OSSUploadObjectPath).mockResolvedValue("" as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    // put the tab into a selected-bucket state so the breadcrumb renders
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              currentBucket: "b1",
              currentPrefix: "docs/",
              listing: { objects: [], prefixes: [], truncated: false, cursor: "" },
            },
          },
        }) as never
    );
    fireEvent.click(await screen.findByTestId("oss-upload"));
    await waitFor(() => expect(OSSUploadObjectPath).toHaveBeenCalledWith(7, "b1", "docs/a.txt", "/local/a.txt"));
  });

  it.each(["list", "grid"] as const)("downloads a folder of the current bucket from the %s view", async (viewMode) => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    vi.mocked(OSSDownloadFolder).mockResolvedValue("" as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              viewMode,
              currentBucket: "b1",
              currentPrefix: "docs/",
              listing: { objects: [], prefixes: ["docs/sub/"], truncated: false, cursor: "" },
            },
          },
        }) as never
    );
    const testId = viewMode === "grid" ? "oss-grid-download-docs/sub/" : "oss-download-docs/sub/";
    fireEvent.click(await screen.findByTestId(testId));
    await waitFor(() => expect(OSSDownloadFolder).toHaveBeenCalledWith(7, "b1", "docs/sub/"));
  });

  it("opens an in-app dialog and creates a folder", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    vi.mocked(OSSCreateFolder).mockResolvedValue(undefined as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              currentBucket: "b1",
              currentPrefix: "docs/",
              listing: { objects: [], prefixes: [], truncated: false, cursor: "" },
            },
          },
        }) as never
    );

    fireEvent.click(await screen.findByTestId("oss-new-folder"));
    fireEvent.change(await screen.findByPlaceholderText("oss.browser.newFolderPrompt"), {
      target: { value: "images" },
    });
    fireEvent.click(screen.getByText("action.ok"));

    await waitFor(() =>
      expect(OSSCreateFolder).toHaveBeenCalledWith({ assetId: 7, bucket: "b1", prefix: "docs/images/" })
    );
  });

  it("expands the left tree when entering a directory from the right list", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    vi.mocked(OSSListObjects)
      .mockResolvedValueOnce({
        prefixes: ["docs/"],
        objects: [],
        nextContinuationToken: "",
        isTruncated: false,
      } as never)
      .mockResolvedValueOnce({
        prefixes: ["docs/sub/"],
        objects: [],
        nextContinuationToken: "",
        isTruncated: false,
      } as never);

    render(<OSSBrowserPanel tabId={TAB} />);
    fireEvent.click(await screen.findByTestId("oss-bucket-b1"));
    fireEvent.doubleClick(await screen.findByTestId("oss-folder-docs/"));

    expect(await screen.findByTestId("oss-tree-row-docs/sub/")).toBeInTheDocument();
  });

  it("toggles to grid view and opens the detail pane on single-click", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              currentBucket: "b1",
              currentPrefix: "docs/",
              listing: {
                objects: [
                  {
                    key: "docs/a.txt",
                    size: 1,
                    lastModified: 0,
                    etag: "",
                    storageClass: "",
                    contentType: "",
                    isPrefix: false,
                  },
                ],
                prefixes: [],
                truncated: false,
                cursor: "",
              },
            },
          },
        }) as never
    );
    // switch to grid
    fireEvent.click(await screen.findByTestId("oss-view-grid"));
    expect(await screen.findByTestId("oss-object-grid")).toBeInTheDocument();
    // focus the object → detail pane opens
    fireEvent.click(screen.getByTestId("oss-grid-object-docs/a.txt"));
    expect(await screen.findByTestId("oss-object-detail")).toBeInTheDocument();
  });

  it("hides the selection bar in grid mode but keeps the selection itself", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              currentBucket: "b1",
              currentPrefix: "docs/",
              viewMode: "list",
              selection: new Set(["docs/a.txt"]),
              listing: {
                objects: [
                  {
                    key: "docs/a.txt",
                    size: 1,
                    lastModified: 0,
                    etag: "",
                    storageClass: "",
                    contentType: "",
                    isPrefix: false,
                  },
                ],
                prefixes: [],
                truncated: false,
                cursor: "",
              },
            },
          },
        }) as never
    );
    // list mode: selection bar is visible
    expect(await screen.findByTestId("oss-selection-bar")).toBeInTheDocument();

    // switch to grid: selection bar hides, but selection state is untouched
    fireEvent.click(screen.getByTestId("oss-view-grid"));
    await screen.findByTestId("oss-object-grid");
    expect(screen.queryByTestId("oss-selection-bar")).not.toBeInTheDocument();
    expect(useOssBrowserStore.getState().tabs[TAB]?.selection).toEqual(new Set(["docs/a.txt"]));
  });

  describe("destination name conflicts", () => {
    const obj = (key: string) => ({
      key,
      size: 5,
      lastModified: 1700000000,
      etag: "",
      storageClass: "",
      contentType: "",
      isPrefix: false,
    });
    const emptyPage = { prefixes: [], objects: [], nextContinuationToken: "", isTruncated: false };

    async function openDetail(existingKeys: string[]) {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
      vi.mocked(OSSCopyObject)
        .mockReset()
        .mockResolvedValue(undefined as never);
      vi.mocked(OSSMoveObject)
        .mockReset()
        .mockResolvedValue(undefined as never);
      vi.mocked(OSSListObjects)
        .mockReset()
        .mockImplementation((async (req: { prefix: string; maxKeys: number }) =>
          req.maxKeys === 1 && existingKeys.includes(req.prefix)
            ? { ...emptyPage, objects: [obj(req.prefix)] }
            : emptyPage) as never);
      render(<OSSBrowserPanel tabId={TAB} />);
      await screen.findByTestId("oss-bucket-b1");
      useOssBrowserStore.setState(
        (s) =>
          ({
            tabs: {
              ...s.tabs,
              [TAB]: {
                ...s.tabs[TAB],
                currentBucket: "b1",
                currentPrefix: "docs/",
                focusedKey: "docs/a.txt",
                listing: { objects: [obj("docs/a.txt")], prefixes: [], truncated: false, cursor: "" },
              },
            },
          }) as never
      );
      await screen.findByTestId("oss-object-detail");
    }

    it.each([
      ["copy", "oss-detail-copy", OSSCopyObject],
      ["move", "oss-detail-move", OSSMoveObject],
    ] as const)("%s to an existing key asks first and skip changes nothing", async (_mode, testId, binding) => {
      vi.stubGlobal("prompt", vi.fn().mockReturnValue("docs/b.txt"));
      await openDetail(["docs/b.txt"]);
      fireEvent.click(screen.getByTestId(testId));
      expect(await screen.findByText("fileConflict.title")).toBeInTheDocument();
      expect(screen.getByText("docs/b.txt")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "fileConflict.skip" }));
      await waitFor(() => expect(screen.queryByText("fileConflict.title")).not.toBeInTheDocument());
      expect(binding).not.toHaveBeenCalled();
    });

    it("overwrite sends the requested destination key", async () => {
      vi.stubGlobal("prompt", vi.fn().mockReturnValue("docs/b.txt"));
      await openDetail(["docs/b.txt"]);
      fireEvent.click(screen.getByTestId("oss-detail-copy"));
      fireEvent.click(await screen.findByRole("button", { name: "fileConflict.overwrite" }));
      await waitFor(() =>
        expect(OSSCopyObject).toHaveBeenCalledWith(
          expect.objectContaining({ srcKey: "docs/a.txt", dstKey: "docs/b.txt" })
        )
      );
    });

    it("keep both lands on the first free _copy name", async () => {
      vi.stubGlobal("prompt", vi.fn().mockReturnValue("docs/b.txt"));
      await openDetail(["docs/b.txt", "docs/b_copy.txt"]);
      fireEvent.click(screen.getByTestId("oss-detail-move"));
      fireEvent.click(await screen.findByRole("button", { name: "fileConflict.keepBoth" }));
      await waitFor(() =>
        expect(OSSMoveObject).toHaveBeenCalledWith(expect.objectContaining({ dstKey: "docs/b_copy2.txt" }))
      );
    });

    it("a free destination goes straight through without a dialog", async () => {
      vi.stubGlobal("prompt", vi.fn().mockReturnValue("docs/new.txt"));
      await openDetail([]);
      fireEvent.click(screen.getByTestId("oss-detail-copy"));
      await waitFor(() =>
        expect(OSSCopyObject).toHaveBeenCalledWith(expect.objectContaining({ dstKey: "docs/new.txt" }))
      );
      expect(screen.queryByText("fileConflict.title")).not.toBeInTheDocument();
    });

    it("a failed existence check aborts instead of overwriting", async () => {
      vi.stubGlobal("prompt", vi.fn().mockReturnValue("docs/b.txt"));
      await openDetail([]);
      vi.mocked(OSSListObjects).mockReset().mockRejectedValue(new Error("AccessDenied"));
      fireEvent.click(screen.getByTestId("oss-detail-copy"));
      await waitFor(() => expect(OSSListObjects).toHaveBeenCalled());
      await new Promise((r) => setTimeout(r, 20));
      expect(OSSCopyObject).not.toHaveBeenCalled();
      expect(screen.queryByText("fileConflict.title")).not.toBeInTheDocument();
    });

    async function openUploadWithExisting(existing: string[]) {
      await openDetail(existing);
      vi.mocked(OSSPickUploadFiles)
        .mockReset()
        .mockResolvedValue(["/local/b.txt"] as never);
      vi.mocked(OSSUploadObjectPath)
        .mockReset()
        .mockResolvedValue("" as never);
      vi.mocked(StatLocalPaths)
        .mockReset()
        .mockResolvedValue([{ path: "/local/b.txt", isDir: false, size: 3072, modTime: 1_710_000_000_000 }]);
    }

    it("toolbar upload conflict shows the local file's size and modified time", async () => {
      await openUploadWithExisting(["docs/b.txt"]);
      fireEvent.click(screen.getByTestId("oss-upload"));
      const dialog = await screen.findByTestId("file-conflict-dialog");
      expect(StatLocalPaths).toHaveBeenCalledWith(["/local/b.txt"]);
      expect(within(dialog).getByText("3.0 KB")).toBeInTheDocument();
      expect(within(dialog).getByText(new Date(1_710_000_000_000).toLocaleString())).toBeInTheDocument();
      expect(within(dialog).queryByText("fileConflict.unknown")).toBeNull();
    });

    it("toolbar upload onto an existing key asks first and skip uploads nothing", async () => {
      await openUploadWithExisting(["docs/b.txt"]);
      fireEvent.click(screen.getByTestId("oss-upload"));
      expect(await screen.findByText("fileConflict.title")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "fileConflict.skip" }));
      await waitFor(() => expect(screen.queryByText("fileConflict.title")).not.toBeInTheDocument());
      expect(OSSUploadObjectPath).not.toHaveBeenCalled();
    });

    it("toolbar upload with keep both uploads to the _copy key", async () => {
      await openUploadWithExisting(["docs/b.txt"]);
      fireEvent.click(screen.getByTestId("oss-upload"));
      fireEvent.click(await screen.findByRole("button", { name: "fileConflict.keepBoth" }));
      await waitFor(() => expect(OSSUploadObjectPath).toHaveBeenCalledWith(7, "b1", "docs/b_copy.txt", "/local/b.txt"));
    });

    it("toolbar upload with a failed existence check uploads nothing", async () => {
      await openUploadWithExisting([]);
      vi.mocked(OSSListObjects).mockReset().mockRejectedValue(new Error("AccessDenied"));
      fireEvent.click(screen.getByTestId("oss-upload"));
      await waitFor(() => expect(OSSListObjects).toHaveBeenCalled());
      await new Promise((r) => setTimeout(r, 20));
      expect(OSSUploadObjectPath).not.toHaveBeenCalled();
      expect(screen.queryByText("fileConflict.title")).not.toBeInTheDocument();
    });
  });
  it("batch delete confirm states the count and irreversibility, with a verb button", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    const obj = (key: string) => ({
      key,
      size: 1,
      lastModified: 0,
      etag: "",
      storageClass: "",
      contentType: "",
      isPrefix: false,
    });
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              currentBucket: "b1",
              currentPrefix: "docs/",
              viewMode: "list",
              selection: new Set(["docs/a.txt", "docs/b.txt", "docs/c.txt"]),
              listing: {
                objects: [obj("docs/a.txt"), obj("docs/b.txt"), obj("docs/c.txt")],
                prefixes: [],
                truncated: false,
                cursor: "",
              },
            },
          },
        }) as never
    );
    const bar = await screen.findByTestId("oss-selection-bar");
    fireEvent.click(within(bar).getByRole("button"));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("oss.browser.deleteConfirmManyIrreversible");
    expect(within(dialog).getByRole("button", { name: "action.delete" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "action.confirm" })).not.toBeInTheDocument();
  });
});
