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
    expect(useEtcdStore.getState().treeCache.get("/")?.length).toBe(1);
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
    const nodes = useEtcdStore.getState().treeCache.get("/root/")!;
    expect(nodes).toEqual([
      { prefix: "/root/svc/", name: "svc", isLeaf: false },
      { prefix: "/root/app/", name: "app", isLeaf: false },
      { prefix: "/root/version", name: "version", isLeaf: true },
    ]);
    expect(useEtcdStore.getState().truncatedAt.get("/root/")).toBe(true);
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

  it("invalidate without prefix clears all", () => {
    useEtcdStore.setState({
      treeCache: new Map([["/", []]]),
      truncatedAt: new Map([["/", true]]),
    });
    useEtcdStore.getState().invalidate();
    expect(useEtcdStore.getState().treeCache.size).toBe(0);
    expect(useEtcdStore.getState().truncatedAt.size).toBe(0);
  });

  it("invalidate with prefix clears only that prefix", () => {
    useEtcdStore.setState({
      treeCache: new Map([
        ["/a/", []],
        ["/b/", []],
      ]),
      truncatedAt: new Map([
        ["/a/", true],
        ["/b/", false],
      ]),
    });
    useEtcdStore.getState().invalidate("/a/");
    expect(useEtcdStore.getState().treeCache.has("/a/")).toBe(false);
    expect(useEtcdStore.getState().treeCache.has("/b/")).toBe(true);
    expect(useEtcdStore.getState().truncatedAt.has("/a/")).toBe(false);
    expect(useEtcdStore.getState().truncatedAt.has("/b/")).toBe(true);
  });

  it("testConnection calls EtcdTestConnection with assetId", async () => {
    const { EtcdTestConnection } = await import("../../wailsjs/go/etcd/Etcd");
    await useEtcdStore.getState().testConnection(42);
    expect(EtcdTestConnection).toHaveBeenCalledWith(42);
  });
});
