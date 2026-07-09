import { create } from "zustand";
import {
  OSSListBuckets,
  OSSListObjects,
  OSSRemoveObject,
  OSSRemoveObjects,
  OSSPresignGet,
} from "../../wailsjs/go/oss/OSS";
import { oss_svc } from "../../wailsjs/go/models";
import { registerTabCloseHook, type QueryTabMeta } from "./tabStore";
import type { OssPrefixNode } from "@/lib/ossPrefixTree";

const OSS_PAGE_SIZE = 200;

export interface OssListing {
  objects: oss_svc.ObjectItem[];
  prefixes: string[];
  cursor: string;
  truncated: boolean;
}

export interface OssBrowserTabState {
  assetId: number;
  buckets: oss_svc.BucketItem[] | null;
  currentBucket: string;
  currentPrefix: string;
  tree: Record<string, OssPrefixNode>;
  expanded: Set<string>;
  listing: OssListing | null;
  selection: Set<string>;
  loading: { buckets: boolean; listing: boolean; page: boolean };
  error: string | null;
  viewMode: "list" | "grid";
  focusedKey: string | null;
  thumbnails: Record<string, string>;
}

interface OssBrowserState {
  tabs: Record<string, OssBrowserTabState>;
  loadBuckets: (tabId: string, assetId: number) => Promise<void>;
  selectBucket: (tabId: string, bucket: string) => Promise<void>;
  navigateToPrefix: (tabId: string, prefix: string) => Promise<void>;
  expandNode: (tabId: string, prefix: string) => Promise<void>;
  loadNextPage: (tabId: string) => Promise<void>;
  toggleSelect: (tabId: string, key: string) => void;
  clearSelection: (tabId: string) => void;
  deleteSelected: (tabId: string) => Promise<void>;
  refresh: (tabId: string) => Promise<void>;
  setViewMode: (tabId: string, mode: "list" | "grid") => void;
  focusObject: (tabId: string, key: string | null) => void;
  ensureThumbnail: (tabId: string, key: string) => Promise<void>;
  deleteObject: (tabId: string, key: string) => Promise<void>;
}

function emptyTabState(assetId: number): OssBrowserTabState {
  return {
    assetId,
    buckets: null,
    currentBucket: "",
    currentPrefix: "",
    tree: {},
    expanded: new Set(),
    listing: null,
    selection: new Set(),
    loading: { buckets: false, listing: false, page: false },
    error: null,
    viewMode: "list",
    focusedKey: null,
    thumbnails: {},
  };
}

// 并发 ensureThumbnail 去重守卫；非 store 字段，避免为预览状态触发全量重渲染。
const thumbInFlight = new Set<string>(); // `${tabId}:${key}`

export const useOssBrowserStore = create<OssBrowserState>((set, get) => {
  // 只在同一 tab 存在时打补丁；不存在则整体不变（避免为已关闭 tab 重建 slice）。
  const patch = (tabId: string, fn: (t: OssBrowserTabState) => OssBrowserTabState) =>
    set((s) => (s.tabs[tabId] ? { tabs: { ...s.tabs, [tabId]: fn(s.tabs[tabId]) } } : { tabs: s.tabs }));

  const listInto = (tabId: string, prefix: string, continuationToken: string): Promise<oss_svc.ListObjectsResult> => {
    const t = get().tabs[tabId];
    return OSSListObjects({
      assetId: t.assetId,
      bucket: t.currentBucket,
      prefix,
      maxKeys: OSS_PAGE_SIZE,
      continuationToken,
    });
  };

  return {
    tabs: {},

    loadBuckets: async (tabId, assetId) => {
      set((s) => ({
        tabs: {
          ...s.tabs,
          [tabId]: {
            ...(s.tabs[tabId] ?? emptyTabState(assetId)),
            assetId,
            loading: { ...(s.tabs[tabId]?.loading ?? emptyTabState(assetId).loading), buckets: true },
            error: null,
          },
        },
      }));
      try {
        const buckets = await OSSListBuckets(assetId);
        patch(tabId, (t) => ({ ...t, buckets: buckets ?? [], loading: { ...t.loading, buckets: false } }));
      } catch (err) {
        patch(tabId, (t) => ({ ...t, loading: { ...t.loading, buckets: false }, error: String(err) }));
        throw err;
      }
    },

    selectBucket: async (tabId, bucket) => {
      patch(tabId, (t) => ({
        ...t,
        currentBucket: bucket,
        currentPrefix: "",
        tree: {},
        expanded: new Set(),
        listing: null,
        selection: new Set(),
        focusedKey: null,
      }));
      await get().navigateToPrefix(tabId, "");
    },

    navigateToPrefix: async (tabId, prefix) => {
      if (!get().tabs[tabId]) return;
      patch(tabId, (t) => ({
        ...t,
        currentPrefix: prefix,
        selection: new Set(),
        focusedKey: null,
        loading: { ...t.loading, listing: true },
        error: null,
      }));
      try {
        const res = await listInto(tabId, prefix, "");
        patch(tabId, (t) => ({
          ...t,
          listing: {
            objects: res.objects ?? [],
            prefixes: res.prefixes ?? [],
            cursor: res.nextContinuationToken ?? "",
            truncated: !!res.isTruncated,
          },
          tree: {
            ...t.tree,
            [prefix]: {
              childPrefixes: res.prefixes ?? [],
              loaded: true,
              cursor: res.nextContinuationToken ?? "",
              truncated: !!res.isTruncated,
            },
          },
          loading: { ...t.loading, listing: false },
        }));
      } catch (err) {
        patch(tabId, (t) => ({ ...t, loading: { ...t.loading, listing: false }, error: String(err) }));
        throw err;
      }
    },

    expandNode: async (tabId, prefix) => {
      const t0 = get().tabs[tabId];
      if (!t0) return;
      const wasExpanded = t0.expanded.has(prefix);
      patch(tabId, (t) => {
        const expanded = new Set(t.expanded);
        if (wasExpanded) expanded.delete(prefix);
        else expanded.add(prefix);
        return { ...t, expanded };
      });
      if (wasExpanded || t0.tree[prefix]?.loaded) return; // collapse, or already lazily loaded
      try {
        const res = await listInto(tabId, prefix, "");
        patch(tabId, (t) => ({
          ...t,
          tree: {
            ...t.tree,
            [prefix]: {
              childPrefixes: res.prefixes ?? [],
              loaded: true,
              cursor: res.nextContinuationToken ?? "",
              truncated: !!res.isTruncated,
            },
          },
        }));
      } catch (err) {
        patch(tabId, (t) => ({ ...t, error: String(err) }));
        throw err;
      }
    },

    loadNextPage: async (tabId) => {
      const t0 = get().tabs[tabId];
      if (!t0 || !t0.listing || !t0.listing.truncated || t0.loading.page) return;
      const cursor = t0.listing.cursor;
      const prefix = t0.currentPrefix;
      patch(tabId, (t) => ({ ...t, loading: { ...t.loading, page: true } }));
      try {
        const res = await listInto(tabId, prefix, cursor);
        patch(tabId, (t) => ({
          ...t,
          listing: t.listing
            ? {
                objects: [...t.listing.objects, ...(res.objects ?? [])],
                prefixes: [...t.listing.prefixes, ...(res.prefixes ?? [])],
                cursor: res.nextContinuationToken ?? "",
                truncated: !!res.isTruncated,
              }
            : t.listing,
          loading: { ...t.loading, page: false },
        }));
      } catch (err) {
        patch(tabId, (t) => ({ ...t, loading: { ...t.loading, page: false }, error: String(err) }));
        throw err;
      }
    },

    toggleSelect: (tabId, key) => {
      patch(tabId, (t) => {
        const selection = new Set(t.selection);
        if (selection.has(key)) selection.delete(key);
        else selection.add(key);
        return { ...t, selection };
      });
    },

    clearSelection: (tabId) => patch(tabId, (t) => ({ ...t, selection: new Set() })),

    deleteSelected: async (tabId) => {
      const t0 = get().tabs[tabId];
      if (!t0 || t0.selection.size === 0) return;
      const keys = Array.from(t0.selection);
      try {
        if (keys.length === 1) {
          await OSSRemoveObject({ assetId: t0.assetId, bucket: t0.currentBucket, key: keys[0] });
        } else {
          await OSSRemoveObjects({ assetId: t0.assetId, bucket: t0.currentBucket, keys });
        }
      } catch (err) {
        patch(tabId, (t) => ({ ...t, error: String(err) }));
        throw err;
      }
      get().clearSelection(tabId);
      await get().refresh(tabId);
    },

    refresh: async (tabId) => {
      const t0 = get().tabs[tabId];
      if (!t0) return;
      patch(tabId, (t) => {
        const tree = { ...t.tree };
        delete tree[t.currentPrefix];
        return { ...t, tree };
      });
      await get().navigateToPrefix(tabId, get().tabs[tabId]!.currentPrefix);
    },

    setViewMode: (tabId, mode) => patch(tabId, (t) => ({ ...t, viewMode: mode })),

    focusObject: (tabId, key) => patch(tabId, (t) => ({ ...t, focusedKey: key })),

    ensureThumbnail: async (tabId, key) => {
      const t0 = get().tabs[tabId];
      if (!t0 || t0.thumbnails[key]) return; // 已缓存
      const flightKey = `${tabId}:${key}`;
      if (thumbInFlight.has(flightKey)) return; // 生成中
      thumbInFlight.add(flightKey);
      try {
        const url = await OSSPresignGet({ assetId: t0.assetId, bucket: t0.currentBucket, key, expirySecs: 0 });
        if (url) patch(tabId, (t) => ({ ...t, thumbnails: { ...t.thumbnails, [key]: url } }));
      } catch {
        // 缩略图为尽力而为的预览，presign 失败静默回退到类型图标（唯一豁免的吞错点，见 spec §2.9）
      } finally {
        thumbInFlight.delete(flightKey);
      }
    },

    deleteObject: async (tabId, key) => {
      const t0 = get().tabs[tabId];
      if (!t0) return;
      try {
        await OSSRemoveObject({ assetId: t0.assetId, bucket: t0.currentBucket, key });
      } catch (err) {
        patch(tabId, (t) => ({ ...t, error: String(err) }));
        throw err;
      }
      patch(tabId, (t) => (t.focusedKey === key ? { ...t, focusedKey: null } : t));
      await get().refresh(tabId);
    },
  };
});

// tab 关闭时清理该 OSS query tab 的浏览态（仿 queryStore / sftpStore 的 close hook）。
registerTabCloseHook((tab) => {
  if (tab.type !== "query") return;
  if ((tab.meta as QueryTabMeta).assetType !== "oss") return;
  useOssBrowserStore.setState((s) => {
    const next = { ...s.tabs };
    delete next[tab.id];
    return { tabs: next };
  });
});
