/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../../wailsjs/go/oss/OSS", () => ({
  OSSListBuckets: vi.fn(),
  OSSListObjects: vi.fn(),
  OSSRemoveObject: vi.fn(),
  OSSRemoveObjects: vi.fn(),
  OSSPresignGet: vi.fn(),
}));

import {
  OSSListBuckets,
  OSSListObjects,
  OSSRemoveObject,
  OSSRemoveObjects,
  OSSPresignGet,
} from "../../wailsjs/go/oss/OSS";
import { useOssBrowserStore } from "./ossBrowserStore";
import { useTabStore } from "./tabStore";

const TAB = "query-7";

function obj(key: string, size = 1): any {
  return { key, size, lastModified: 0, etag: "", storageClass: "STANDARD", contentType: "", isPrefix: false };
}

// 已选桶的基础 tab 态（selection/delete/close-hook 用例直接 setState 灌入）。
function baseState(over: Partial<any> = {}): any {
  return {
    assetId: 7,
    buckets: [{ name: "b1", creationDate: 0 }],
    currentBucket: "b1",
    currentPrefix: "",
    tree: { "": { childPrefixes: [], loaded: true, cursor: "", truncated: false } },
    expanded: new Set(),
    listing: { objects: [], prefixes: [], cursor: "", truncated: false },
    selection: new Set(),
    loading: { buckets: false, listing: false, page: false },
    error: null,
    ...over,
  };
}

beforeEach(() => {
  vi.mocked(OSSListBuckets).mockReset();
  vi.mocked(OSSListObjects).mockReset();
  vi.mocked(OSSRemoveObject).mockReset();
  vi.mocked(OSSRemoveObjects).mockReset();
  vi.mocked(OSSPresignGet).mockReset();
  useOssBrowserStore.setState({ tabs: {} });
  useTabStore.setState({ tabs: [], activeTabId: null });
});

describe("loadBuckets", () => {
  it("stores buckets and clears the loading flag", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
    await useOssBrowserStore.getState().loadBuckets(TAB, 7);
    const s = useOssBrowserStore.getState().tabs[TAB];
    expect(OSSListBuckets).toHaveBeenCalledWith(7);
    expect(s.buckets).toEqual([{ name: "b1", creationDate: 0 }]);
    expect(s.loading.buckets).toBe(false);
  });

  it("records error and rethrows on binder failure", async () => {
    vi.mocked(OSSListBuckets).mockRejectedValue(new Error("boom"));
    await expect(useOssBrowserStore.getState().loadBuckets(TAB, 7)).rejects.toThrow("boom");
    const s = useOssBrowserStore.getState().tabs[TAB];
    expect(s.error).toContain("boom");
    expect(s.loading.buckets).toBe(false);
  });
});

describe("selectBucket + navigateToPrefix", () => {
  it("selects a bucket, lists its root, fills tree + listing", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
    vi.mocked(OSSListObjects).mockResolvedValue({
      prefixes: ["docs/"],
      objects: [obj("readme.txt")],
      nextContinuationToken: "",
      isTruncated: false,
    } as any);
    const st = useOssBrowserStore.getState();
    await st.loadBuckets(TAB, 7);
    await st.selectBucket(TAB, "b1");
    const s = useOssBrowserStore.getState().tabs[TAB];
    expect(OSSListObjects).toHaveBeenCalledWith({
      assetId: 7,
      bucket: "b1",
      prefix: "",
      maxKeys: 200,
      continuationToken: "",
    });
    expect(s.currentBucket).toBe("b1");
    expect(s.currentPrefix).toBe("");
    expect(s.listing?.objects.map((o: any) => o.key)).toEqual(["readme.txt"]);
    expect(s.tree[""].childPrefixes).toEqual(["docs/"]);
  });
});

describe("loadNextPage cursor pagination", () => {
  it("appends objects (does not overwrite) and advances the cursor", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
    vi.mocked(OSSListObjects)
      .mockResolvedValueOnce({
        prefixes: [],
        objects: [obj("a")],
        nextContinuationToken: "C1",
        isTruncated: true,
      } as any)
      .mockResolvedValueOnce({
        prefixes: [],
        objects: [obj("b")],
        nextContinuationToken: "",
        isTruncated: false,
      } as any);
    const st = useOssBrowserStore.getState();
    await st.loadBuckets(TAB, 7);
    await st.selectBucket(TAB, "b1");
    await st.loadNextPage(TAB);
    const s = useOssBrowserStore.getState().tabs[TAB];
    expect(OSSListObjects).toHaveBeenLastCalledWith({
      assetId: 7,
      bucket: "b1",
      prefix: "",
      maxKeys: 200,
      continuationToken: "C1",
    });
    expect(s.listing?.objects.map((o: any) => o.key)).toEqual(["a", "b"]);
    expect(s.listing?.truncated).toBe(false);
    expect(s.listing?.cursor).toBe("");
  });

  it("no-ops when the current listing is not truncated", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
    vi.mocked(OSSListObjects).mockResolvedValue({
      prefixes: [],
      objects: [obj("a")],
      nextContinuationToken: "",
      isTruncated: false,
    } as any);
    const st = useOssBrowserStore.getState();
    await st.loadBuckets(TAB, 7);
    await st.selectBucket(TAB, "b1");
    vi.mocked(OSSListObjects).mockClear();
    await st.loadNextPage(TAB);
    expect(OSSListObjects).not.toHaveBeenCalled();
  });
});

describe("expandNode", () => {
  it("lazily loads a node's child prefixes once, and collapses without refetch", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
    vi.mocked(OSSListObjects)
      .mockResolvedValueOnce({ prefixes: ["docs/"], objects: [], nextContinuationToken: "", isTruncated: false } as any)
      .mockResolvedValueOnce({
        prefixes: ["docs/sub/"],
        objects: [],
        nextContinuationToken: "",
        isTruncated: false,
      } as any);
    const st = useOssBrowserStore.getState();
    await st.loadBuckets(TAB, 7);
    await st.selectBucket(TAB, "b1"); // fills tree[""]
    await st.expandNode(TAB, "docs/"); // loads tree["docs/"]
    let s = useOssBrowserStore.getState().tabs[TAB];
    expect(s.expanded.has("docs/")).toBe(true);
    expect(s.tree["docs/"].childPrefixes).toEqual(["docs/sub/"]);
    const callsAfterLoad = vi.mocked(OSSListObjects).mock.calls.length;
    await st.expandNode(TAB, "docs/"); // collapse — no fetch
    s = useOssBrowserStore.getState().tabs[TAB];
    expect(s.expanded.has("docs/")).toBe(false);
    expect(vi.mocked(OSSListObjects).mock.calls.length).toBe(callsAfterLoad);
  });
});

describe("selection + delete", () => {
  it("toggleSelect adds then removes a key", () => {
    useOssBrowserStore.setState({ tabs: { [TAB]: baseState() } });
    const st = useOssBrowserStore.getState();
    st.toggleSelect(TAB, "a");
    expect(useOssBrowserStore.getState().tabs[TAB].selection.has("a")).toBe(true);
    st.toggleSelect(TAB, "a");
    expect(useOssBrowserStore.getState().tabs[TAB].selection.has("a")).toBe(false);
  });

  it("deleteSelected with one key calls OSSRemoveObject, reloads, clears selection", async () => {
    useOssBrowserStore.setState({ tabs: { [TAB]: baseState({ selection: new Set(["docs/a"]) }) } });
    vi.mocked(OSSListObjects).mockResolvedValue({
      prefixes: [],
      objects: [],
      nextContinuationToken: "",
      isTruncated: false,
    } as any);
    await useOssBrowserStore.getState().deleteSelected(TAB);
    expect(OSSRemoveObject).toHaveBeenCalledWith({ assetId: 7, bucket: "b1", key: "docs/a" });
    expect(OSSRemoveObjects).not.toHaveBeenCalled();
    expect(OSSListObjects).toHaveBeenCalled(); // refresh re-listed current prefix
    expect(useOssBrowserStore.getState().tabs[TAB].selection.size).toBe(0);
  });

  it("deleteSelected with many keys calls OSSRemoveObjects", async () => {
    useOssBrowserStore.setState({ tabs: { [TAB]: baseState({ selection: new Set(["a", "b"]) }) } });
    vi.mocked(OSSListObjects).mockResolvedValue({
      prefixes: [],
      objects: [],
      nextContinuationToken: "",
      isTruncated: false,
    } as any);
    await useOssBrowserStore.getState().deleteSelected(TAB);
    expect(OSSRemoveObjects).toHaveBeenCalledWith({ assetId: 7, bucket: "b1", keys: ["a", "b"] });
  });

  it("deleteSelected rethrows and preserves selection on binder failure", async () => {
    useOssBrowserStore.setState({ tabs: { [TAB]: baseState({ selection: new Set(["a"]) }) } });
    vi.mocked(OSSRemoveObject).mockRejectedValue(new Error("denied"));
    await expect(useOssBrowserStore.getState().deleteSelected(TAB)).rejects.toThrow("denied");
    expect(useOssBrowserStore.getState().tabs[TAB].selection.has("a")).toBe(true);
  });
});

describe("tab close hook", () => {
  it("drops the tab slice when an oss query tab is closed", () => {
    useOssBrowserStore.setState({ tabs: { [TAB]: baseState() } });
    useTabStore.setState({
      tabs: [
        {
          id: TAB,
          type: "query",
          label: "b1",
          meta: { type: "query", assetId: 7, assetName: "b1", assetIcon: "", assetType: "oss" },
        },
      ],
      activeTabId: TAB,
    });
    useTabStore.getState().closeTab(TAB);
    expect(useOssBrowserStore.getState().tabs[TAB]).toBeUndefined();
  });
});

describe("ossBrowserStore P3b-3 additions", () => {
  const TAB = "query-detail";
  function seed(over: Partial<import("./ossBrowserStore").OssBrowserTabState> = {}) {
    useOssBrowserStore.setState({
      tabs: {
        [TAB]: {
          assetId: 7,
          buckets: [],
          currentBucket: "b",
          currentPrefix: "docs/",
          tree: {},
          expanded: new Set(),
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
            ] as never,
            prefixes: [],
            cursor: "",
            truncated: false,
          },
          selection: new Set(),
          loading: { buckets: false, listing: false, page: false },
          error: null,
          viewMode: "list",
          focusedKey: null,
          thumbnails: {},
          ...over,
        } as never,
      },
    } as never);
  }

  it("setViewMode flips the tab's view mode", () => {
    seed();
    useOssBrowserStore.getState().setViewMode(TAB, "grid");
    expect(useOssBrowserStore.getState().tabs[TAB].viewMode).toBe("grid");
  });

  it("focusObject sets and clears the focused key", () => {
    seed();
    useOssBrowserStore.getState().focusObject(TAB, "docs/a.txt");
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBe("docs/a.txt");
    useOssBrowserStore.getState().focusObject(TAB, null);
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBeNull();
  });

  it("ensureThumbnail presigns once and caches the URL", async () => {
    seed();
    vi.mocked(OSSPresignGet).mockResolvedValue("https://signed/a" as never);
    await useOssBrowserStore.getState().ensureThumbnail(TAB, "docs/a.txt");
    expect(useOssBrowserStore.getState().tabs[TAB].thumbnails["docs/a.txt"]).toBe("https://signed/a");
    await useOssBrowserStore.getState().ensureThumbnail(TAB, "docs/a.txt"); // cached → no second call
    expect(OSSPresignGet).toHaveBeenCalledTimes(1);
  });

  it("ensureThumbnail leaves no cache entry on presign failure (silent)", async () => {
    seed();
    vi.mocked(OSSPresignGet).mockRejectedValue(new Error("boom") as never);
    await useOssBrowserStore.getState().ensureThumbnail(TAB, "docs/a.txt");
    expect(useOssBrowserStore.getState().tabs[TAB].thumbnails["docs/a.txt"]).toBeUndefined();
  });

  it("deleteObject removes one object, refreshes, and clears focus", async () => {
    seed({ focusedKey: "docs/a.txt" });
    vi.mocked(OSSRemoveObject).mockResolvedValue(undefined as never);
    const refresh = vi.spyOn(useOssBrowserStore.getState(), "refresh").mockResolvedValue(undefined);
    await useOssBrowserStore.getState().deleteObject(TAB, "docs/a.txt");
    expect(OSSRemoveObject).toHaveBeenCalledWith({ assetId: 7, bucket: "b", key: "docs/a.txt" });
    expect(refresh).toHaveBeenCalledWith(TAB);
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBeNull();
    refresh.mockRestore();
  });

  it("navigateToPrefix clears focusedKey", async () => {
    seed({ focusedKey: "docs/a.txt" });
    vi.mocked(OSSListObjects).mockResolvedValue({
      objects: [],
      prefixes: [],
      nextContinuationToken: "",
      isTruncated: false,
    } as never);
    await useOssBrowserStore.getState().navigateToPrefix(TAB, "other/");
    expect(useOssBrowserStore.getState().tabs[TAB].focusedKey).toBeNull();
  });
});
