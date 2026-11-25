import { create } from "zustand";
import { EtcdExec, EtcdListPrefix, EtcdTestConnection } from "../../wailsjs/go/etcd/Etcd";
import type { etcd_svc } from "../../wailsjs/go/models";
import { errorMessage } from "@/lib/errorMessage";
import { registerTabReconnectAdapter } from "@/lib/tabReconnect";
import { registerTabCloseHook, type QueryTabMeta } from "./tabStore";

export type EtcdTreeNode = {
  prefix: string; // 完整 prefix（以 / 结尾代表目录）
  name: string; // 当前层级展示文本
  isLeaf: boolean;
};

export type EtcdExecMeta =
  | { ok: true; elapsedMs: number; count: number; op: string }
  | { ok: false; elapsedMs: number; error: string; op: string };

// treeCache / truncatedAt 的 key 形如 "${assetId}:${prefix}",避免多个 etcd 资产
// 同时打开时 prefix "/" 互相污染(see https://github.com/opskat/opskat PR #129 review)。
export function etcdCacheKey(assetId: number, prefix: string): string {
  return `${assetId}:${prefix}`;
}

interface State {
  treeCache: Map<string, EtcdTreeNode[]>;
  truncatedAt: Map<string, boolean>;
  prefixErrors: Map<string, string>;
  loadingPrefixes: Set<string>;
  refreshingAssets: Set<number>;
  queryHistory: string[];
  lastResult: etcd_svc.ExecResult | null;
  lastMeta: EtcdExecMeta | null;

  loadPrefix: (assetId: number, prefix: string, opts?: { force?: boolean }) => Promise<void>;
  refreshAsset: (assetId: number, prefixes?: Iterable<string>) => Promise<void>;
  /**
   * invalidate(assetId) 清掉该 asset 的所有 prefix 缓存；
   * invalidate(assetId, prefix) 只清该 asset 的单条 prefix。
   */
  invalidate: (assetId: number, prefix?: string) => void;
  getTreeNodes: (assetId: number, prefix: string) => EtcdTreeNode[] | undefined;
  isTruncated: (assetId: number, prefix: string) => boolean | undefined;
  getPrefixError: (assetId: number, prefix: string) => string | null;
  isPrefixLoading: (assetId: number, prefix: string) => boolean;
  exec: (req: etcd_svc.ExecRequest) => Promise<etcd_svc.ExecResult>;
  testConnection: (assetId: number) => Promise<void>;
}

const HISTORY_KEY = "etcd:queryHistory";
const HISTORY_LIMIT = 50;

function loadHistory(): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? arr.slice(0, HISTORY_LIMIT) : [];
  } catch {
    return [];
  }
}

function saveHistory(h: string[]) {
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(h.slice(0, HISTORY_LIMIT)));
  } catch {
    // localStorage quota / 隐私模式忽略
  }
}

export const useEtcdStore = create<State>((set, get) => ({
  treeCache: new Map(),
  truncatedAt: new Map(),
  prefixErrors: new Map(),
  loadingPrefixes: new Set(),
  refreshingAssets: new Set(),
  queryHistory: loadHistory(),
  lastResult: null,
  lastMeta: null,

  async loadPrefix(assetId, prefix, opts) {
    const key = etcdCacheKey(assetId, prefix);
    if (!opts?.force && get().treeCache.has(key)) return;

    set((s) => {
      const loadingPrefixes = new Set(s.loadingPrefixes);
      loadingPrefixes.add(key);
      const prefixErrors = new Map(s.prefixErrors);
      prefixErrors.delete(key);
      return { loadingPrefixes, prefixErrors };
    });

    try {
      const res = await EtcdListPrefix({
        AssetID: assetId,
        Prefix: prefix,
        Delim: "/",
        Limit: 1000,
      } as etcd_svc.ListPrefixRequest);

      const dirs = res.dirs ?? [];
      const leaves = res.leaves ?? [];
      const nodes: EtcdTreeNode[] = [
        ...dirs.map((d) => ({ prefix: prefix + d + "/", name: d, isLeaf: false })),
        ...leaves.map((kv) => ({ prefix: kv.key, name: kv.key.slice(prefix.length), isLeaf: true })),
      ];
      set((s) => {
        const treeCache = new Map(s.treeCache);
        treeCache.set(key, nodes);
        const truncatedAt = new Map(s.truncatedAt);
        truncatedAt.set(key, !!res.truncated);
        const loadingPrefixes = new Set(s.loadingPrefixes);
        loadingPrefixes.delete(key);
        const prefixErrors = new Map(s.prefixErrors);
        prefixErrors.delete(key);
        return { treeCache, truncatedAt, loadingPrefixes, prefixErrors };
      });
    } catch (e) {
      const msg = errorMessage(e);
      set((s) => {
        const loadingPrefixes = new Set(s.loadingPrefixes);
        loadingPrefixes.delete(key);
        const prefixErrors = new Map(s.prefixErrors);
        prefixErrors.set(key, msg);
        return { loadingPrefixes, prefixErrors };
      });
      throw e;
    }
  },

  async refreshAsset(assetId, prefixes) {
    if (get().refreshingAssets.has(assetId)) return;
    set((s) => {
      const refreshingAssets = new Set(s.refreshingAssets);
      refreshingAssets.add(assetId);
      return { refreshingAssets };
    });
    get().invalidate(assetId);
    const targets = new Set<string>(["/", ...(prefixes ?? [])]);
    try {
      await Promise.all(
        [...targets].map((p) =>
          get()
            .loadPrefix(assetId, p, { force: true })
            .catch(() => undefined)
        )
      );
    } finally {
      set((s) => {
        const refreshingAssets = new Set(s.refreshingAssets);
        refreshingAssets.delete(assetId);
        return { refreshingAssets };
      });
    }
  },

  invalidate(assetId, prefix) {
    set((s) => {
      const cache = new Map(s.treeCache);
      const tr = new Map(s.truncatedAt);
      const errs = new Map(s.prefixErrors);
      if (prefix !== undefined) {
        const key = etcdCacheKey(assetId, prefix);
        cache.delete(key);
        tr.delete(key);
        errs.delete(key);
      } else {
        const assetPrefix = `${assetId}:`;
        for (const k of cache.keys()) {
          if (k.startsWith(assetPrefix)) cache.delete(k);
        }
        for (const k of tr.keys()) {
          if (k.startsWith(assetPrefix)) tr.delete(k);
        }
        for (const k of errs.keys()) {
          if (k.startsWith(assetPrefix)) errs.delete(k);
        }
      }
      return { treeCache: cache, truncatedAt: tr, prefixErrors: errs };
    });
  },

  getTreeNodes(assetId, prefix) {
    return get().treeCache.get(etcdCacheKey(assetId, prefix));
  },

  isTruncated(assetId, prefix) {
    return get().truncatedAt.get(etcdCacheKey(assetId, prefix));
  },

  getPrefixError(assetId, prefix) {
    return get().prefixErrors.get(etcdCacheKey(assetId, prefix)) ?? null;
  },

  isPrefixLoading(assetId, prefix) {
    return get().loadingPrefixes.has(etcdCacheKey(assetId, prefix));
  },

  async exec(req) {
    const t0 = performance.now();
    try {
      const res = await EtcdExec(req);
      const elapsedMs = Math.round(performance.now() - t0);
      const label = `${req.Op} ${req.Key ?? ""}`.trim();
      const next = [label, ...get().queryHistory.filter((h) => h !== label)].slice(0, HISTORY_LIMIT);
      saveHistory(next);
      set({
        queryHistory: next,
        lastResult: res,
        lastMeta: { ok: true, elapsedMs, count: Number(res.count ?? 0), op: req.Op },
      });
      return res;
    } catch (e) {
      const elapsedMs = Math.round(performance.now() - t0);
      const msg = errorMessage(e);
      set({
        lastResult: null,
        lastMeta: { ok: false, elapsedMs, error: msg, op: req.Op },
      });
      throw e;
    }
  },

  async testConnection(assetId) {
    await EtcdTestConnection(assetId);
  },
}));

registerTabCloseHook((tab) => {
  if (tab.type !== "query") return;
  const meta = tab.meta as QueryTabMeta;
  if (meta.assetType !== "etcd") return;
  const assetId = meta.assetId;
  useEtcdStore.getState().invalidate(assetId);
  useEtcdStore.setState((s) => {
    const assetPrefix = `${assetId}:`;
    const loadingPrefixes = new Set(s.loadingPrefixes);
    for (const k of loadingPrefixes) {
      if (k.startsWith(assetPrefix)) loadingPrefixes.delete(k);
    }
    const refreshingAssets = new Set(s.refreshingAssets);
    refreshingAssets.delete(assetId);
    return { loadingPrefixes, refreshingAssets };
  });
});

registerTabReconnectAdapter({
  match: (tab) => tab.type === "query" && (tab.meta as QueryTabMeta).assetType === "etcd",
  getPhase: (tab) => {
    const assetId = (tab.meta as QueryTabMeta).assetId;
    const st = useEtcdStore.getState();
    const rootKey = etcdCacheKey(assetId, "/");
    if (st.refreshingAssets.has(assetId) || st.loadingPrefixes.has(rootKey)) return "connecting";
    if (st.prefixErrors.has(rootKey) && !st.treeCache.has(rootKey)) return "failed";
    return "connected";
  },
  reconnect: async (tab) => {
    const assetId = (tab.meta as QueryTabMeta).assetId;
    await useEtcdStore
      .getState()
      .loadPrefix(assetId, "/", { force: true })
      .catch(() => undefined);
  },
});
