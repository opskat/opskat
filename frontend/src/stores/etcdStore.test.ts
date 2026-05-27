/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../../wailsjs/go/etcd/Etcd", () => ({
  EtcdExec: vi.fn().mockResolvedValue({ op: "get", count: 0, kvs: [], revision: 0 }),
  EtcdListPrefix: vi.fn().mockResolvedValue({ dirs: ["config"], leaves: [], truncated: false }),
  EtcdTestConnection: vi.fn().mockResolvedValue(undefined),
}));

import { useEtcdStore } from "./etcdStore";

describe("useEtcdStore", () => {
  beforeEach(async () => {
    const mod = await import("../../wailsjs/go/etcd/Etcd");
    vi.mocked(mod.EtcdExec).mockClear();
    vi.mocked(mod.EtcdListPrefix).mockClear();
    vi.mocked(mod.EtcdTestConnection).mockClear();
    useEtcdStore.setState({
      treeCache: new Map(),
      truncatedAt: new Map(),
      queryHistory: [],
      lastResult: null,
    });
    localStorage.clear();
  });

  it("loadPrefix caches and skips on second call", async () => {
    const { EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    await useEtcdStore.getState().loadPrefix(1, "/");
    await useEtcdStore.getState().loadPrefix(1, "/");
    expect(EtcdListPrefix).toHaveBeenCalledTimes(1);
    // 缓存按 assetId+prefix 维度（独立测试 etcdCacheKey 行为见下）
    expect(useEtcdStore.getState().treeCache.size).toBe(1);
  });

  it("loadPrefix scopes cache per assetId — same prefix on different assets must not collide", async () => {
    const { EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    vi.mocked(EtcdListPrefix)
      .mockResolvedValueOnce({ dirs: ["a"], leaves: [], truncated: false } as any)
      .mockResolvedValueOnce({ dirs: ["b"], leaves: [], truncated: true } as any);

    await useEtcdStore.getState().loadPrefix(1, "/");
    await useEtcdStore.getState().loadPrefix(2, "/");

    // 两次都得真正调到后端,后一次不能拿到 asset 1 的缓存
    expect(EtcdListPrefix).toHaveBeenCalledTimes(2);
    expect(useEtcdStore.getState().treeCache.size).toBe(2);
    expect(useEtcdStore.getState().getTreeNodes(1, "/")?.[0]?.name).toBe("a");
    expect(useEtcdStore.getState().getTreeNodes(2, "/")?.[0]?.name).toBe("b");
    expect(useEtcdStore.getState().isTruncated(1, "/")).toBe(false);
    expect(useEtcdStore.getState().isTruncated(2, "/")).toBe(true);
  });

  it("loadPrefix force reloads", async () => {
    const { EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    await useEtcdStore.getState().loadPrefix(1, "/");
    await useEtcdStore.getState().loadPrefix(1, "/", { force: true });
    expect(EtcdListPrefix).toHaveBeenCalledTimes(2);
  });

  it("loadPrefix builds nodes from dirs and leaves", async () => {
    const { EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    vi.mocked(EtcdListPrefix).mockResolvedValueOnce({
      dirs: ["svc", "app"],
      leaves: [{ key: "/root/version", value: "v1", modRevision: 0, createRevision: 0, version: 0, lease: 0 }],
      truncated: true,
    } as any);
    await useEtcdStore.getState().loadPrefix(7, "/root/");
    const nodes = useEtcdStore.getState().getTreeNodes(7, "/root/")!;
    expect(nodes).toEqual([
      { prefix: "/root/svc/", name: "svc", isLeaf: false },
      { prefix: "/root/app/", name: "app", isLeaf: false },
      { prefix: "/root/version", name: "version", isLeaf: true },
    ]);
    expect(useEtcdStore.getState().isTruncated(7, "/root/")).toBe(true);
  });

  it("exec dedups recent queryHistory and persists", async () => {
    await useEtcdStore.getState().exec({ AssetID: 1, Op: "get", Key: "/x" } as any);
    await useEtcdStore.getState().exec({ AssetID: 1, Op: "get", Key: "/x" } as any);
    expect(useEtcdStore.getState().queryHistory).toEqual(["get /x"]);
    const stored = JSON.parse(localStorage.getItem("etcd:queryHistory")!);
    expect(stored).toEqual(["get /x"]);
  });

  it("exec stores lastResult", async () => {
    const res = await useEtcdStore.getState().exec({ AssetID: 1, Op: "get", Key: "/x" } as any);
    expect(res.op).toBe("get");
    expect(useEtcdStore.getState().lastResult?.op).toBe("get");
  });

  it("invalidate(assetId) clears only that asset's entries", () => {
    useEtcdStore.setState({
      treeCache: new Map([
        ["1:/", []],
        ["1:/a/", []],
        ["2:/", []],
      ]),
      truncatedAt: new Map([
        ["1:/", true],
        ["2:/", false],
      ]),
    });
    useEtcdStore.getState().invalidate(1);
    expect(useEtcdStore.getState().treeCache.has("1:/")).toBe(false);
    expect(useEtcdStore.getState().treeCache.has("1:/a/")).toBe(false);
    expect(useEtcdStore.getState().treeCache.has("2:/")).toBe(true);
    expect(useEtcdStore.getState().truncatedAt.has("1:/")).toBe(false);
    expect(useEtcdStore.getState().truncatedAt.has("2:/")).toBe(true);
  });

  it("invalidate(assetId, prefix) clears only that prefix on that asset", () => {
    useEtcdStore.setState({
      treeCache: new Map([
        ["1:/a/", []],
        ["1:/b/", []],
        ["2:/a/", []],
      ]),
      truncatedAt: new Map([
        ["1:/a/", true],
        ["1:/b/", false],
      ]),
    });
    useEtcdStore.getState().invalidate(1, "/a/");
    expect(useEtcdStore.getState().treeCache.has("1:/a/")).toBe(false);
    expect(useEtcdStore.getState().treeCache.has("1:/b/")).toBe(true);
    expect(useEtcdStore.getState().treeCache.has("2:/a/")).toBe(true);
    expect(useEtcdStore.getState().truncatedAt.has("1:/a/")).toBe(false);
    expect(useEtcdStore.getState().truncatedAt.has("1:/b/")).toBe(true);
  });

  it("testConnection calls EtcdTestConnection with assetId", async () => {
    const { EtcdTestConnection } = await import("../../wailsjs/go/etcd/Etcd");
    await useEtcdStore.getState().testConnection(42);
    expect(EtcdTestConnection).toHaveBeenCalledWith(42);
  });
});
