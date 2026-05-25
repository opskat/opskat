import { create } from "zustand";
import { EtcdExec, EtcdListPrefix, EtcdTestConnection } from "../../wailsjs/go/etcd/Etcd";
import type { etcd_svc } from "../../wailsjs/go/models";

export type EtcdTreeNode = {
  prefix: string; // 完整 prefix（以 / 结尾代表目录）
  name: string; // 当前层级展示文本
  isLeaf: boolean;
};

interface State {
  treeCache: Map<string, EtcdTreeNode[]>;
  truncatedAt: Map<string, boolean>;
  queryHistory: string[];
  lastResult: etcd_svc.ExecResult | null;

  loadPrefix: (assetId: number, prefix: string, opts?: { force?: boolean }) => Promise<void>;
  invalidate: (prefix?: string) => void;
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

  async loadPrefix(assetId, prefix, opts) {
    if (!opts?.force && get().treeCache.has(prefix)) return;
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
    cache.set(prefix, nodes);
    const tr = new Map(get().truncatedAt);
    tr.set(prefix, !!res.truncated);
    set({ treeCache: cache, truncatedAt: tr });
  },

  invalidate(prefix) {
    if (!prefix) {
      set({ treeCache: new Map(), truncatedAt: new Map() });
      return;
    }
    const cache = new Map(get().treeCache);
    cache.delete(prefix);
    const tr = new Map(get().truncatedAt);
    tr.delete(prefix);
    set({ treeCache: cache, truncatedAt: tr });
  },

  async exec(req) {
    const res = await EtcdExec(req);
    const label = `${req.Op} ${req.Key ?? ""}`.trim();
    const next = [label, ...get().queryHistory.filter((h) => h !== label)].slice(0, HISTORY_LIMIT);
    saveHistory(next);
    set({ queryHistory: next, lastResult: res });
    return res;
  },

  async testConnection(assetId) {
    await EtcdTestConnection(assetId);
  },
}));
