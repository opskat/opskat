import { create } from "zustand";
import { EtcdExec, EtcdListPrefix, EtcdTestConnection } from "../../wailsjs/go/etcd/Etcd";
import type { etcd_svc } from "../../wailsjs/go/models";

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
  queryHistory: string[];
  lastResult: etcd_svc.ExecResult | null;
  lastMeta: EtcdExecMeta | null;

  loadPrefix: (assetId: number, prefix: string, opts?: { force?: boolean }) => Promise<void>;
  /**
   * invalidate(assetId) 清掉该 asset 的所有 prefix 缓存；
   * invalidate(assetId, prefix) 只清该 asset 的单条 prefix。
   */
  invalidate: (assetId: number, prefix?: string) => void;
  getTreeNodes: (assetId: number, prefix: string) => EtcdTreeNode[] | undefined;
  isTruncated: (assetId: number, prefix: string) => boolean | undefined;
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
  queryHistory: loadHistory(),
  lastResult: null,
  lastMeta: null,

  async loadPrefix(assetId, prefix, opts) {
    const key = etcdCacheKey(assetId, prefix);
    if (!opts?.force && get().treeCache.has(key)) return;
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
    const cache = new Map(get().treeCache);
    cache.set(key, nodes);
    const tr = new Map(get().truncatedAt);
    tr.set(key, !!res.truncated);
    set({ treeCache: cache, truncatedAt: tr });
  },

  invalidate(assetId, prefix) {
    const cache = new Map(get().treeCache);
    const tr = new Map(get().truncatedAt);
    if (prefix !== undefined) {
      const key = etcdCacheKey(assetId, prefix);
      cache.delete(key);
      tr.delete(key);
    } else {
      const assetPrefix = `${assetId}:`;
      for (const k of cache.keys()) {
        if (k.startsWith(assetPrefix)) cache.delete(k);
      }
      for (const k of tr.keys()) {
        if (k.startsWith(assetPrefix)) tr.delete(k);
      }
    }
    set({ treeCache: cache, truncatedAt: tr });
  },

  getTreeNodes(assetId, prefix) {
    return get().treeCache.get(etcdCacheKey(assetId, prefix));
  },

  isTruncated(assetId, prefix) {
    return get().truncatedAt.get(etcdCacheKey(assetId, prefix));
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
      const msg = e instanceof Error ? e.message : String(e);
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
