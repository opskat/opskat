import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";

vi.mock("../../wailsjs/go/etcd/Etcd", () => ({
  EtcdExec: vi.fn().mockResolvedValue({ op: "get", count: 0, kvs: [], revision: 0 }),
  EtcdListPrefix: vi.fn().mockImplementation(async ({ Prefix }: { Prefix: string }) => {
    if (Prefix === "/") {
      return {
        dirs: ["config", "flags"],
        leaves: [{ key: "/version", modRevision: 1, createRevision: 1, version: 1, lease: 0 }],
        truncated: false,
      };
    }
    if (Prefix === "/config/") {
      return {
        dirs: [],
        leaves: [{ key: "/config/timeout", modRevision: 2, createRevision: 2, version: 1, lease: 0 }],
        truncated: true,
      };
    }
    return { dirs: [], leaves: [], truncated: false };
  }),
  EtcdTestConnection: vi.fn().mockResolvedValue(undefined),
}));

import { EtcdTreePane } from "@/components/etcd/EtcdTreePane";
import { useEtcdStore } from "@/stores/etcdStore";

describe("EtcdTreePane", () => {
  beforeEach(async () => {
    // 每个用例都从空 store 起步，避免 cache 串流
    useEtcdStore.setState({
      treeCache: new Map(),
      truncatedAt: new Map(),
      queryHistory: [],
      lastResult: null,
    });
    // mock 是 module scope，调用计数会跨用例累积 —— 显式清零
    const { EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    (EtcdListPrefix as ReturnType<typeof vi.fn>).mockClear();
  });

  it("loads root prefix on mount and renders dirs + leaves", async () => {
    render(<EtcdTreePane assetId={1} />);
    await waitFor(() => {
      expect(screen.getByText("config")).toBeInTheDocument();
      expect(screen.getByText("flags")).toBeInTheDocument();
      // leaf 在根 prefix "/" 下，name = "version"
      expect(screen.getByText("version")).toBeInTheDocument();
    });
  });

  it("expands a directory on click and lazy-loads children", async () => {
    const { EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    render(<EtcdTreePane assetId={1} />);
    await waitFor(() => expect(screen.getByText("config")).toBeInTheDocument());
    expect(EtcdListPrefix).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByText("config"));
    await waitFor(() => {
      expect(EtcdListPrefix).toHaveBeenCalledTimes(2);
      expect(screen.getByText("timeout")).toBeInTheDocument();
    });

    // 再次点击应折叠，不会触发新的 loadPrefix
    fireEvent.click(screen.getByText("config"));
    expect(EtcdListPrefix).toHaveBeenCalledTimes(2);
  });

  it("renders truncated indicator when child set is capped", async () => {
    render(<EtcdTreePane assetId={1} />);
    await waitFor(() => expect(screen.getByText("config")).toBeInTheDocument());
    fireEvent.click(screen.getByText("config"));
    await waitFor(() => {
      expect(screen.getByTestId("etcd-tree-truncated")).toBeInTheDocument();
    });
  });

  it("filters leaves by name (case-insensitive substring)", async () => {
    render(<EtcdTreePane assetId={1} />);
    await waitFor(() => expect(screen.getByText("version")).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText("etcd.tree.filterPlaceholder"), {
      target: { value: "VER" },
    });
    // "version".toLowerCase().includes("ver") => true
    expect(screen.getByText("version")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("etcd.tree.filterPlaceholder"), {
      target: { value: "zzz-no-match" },
    });
    expect(screen.queryByText("version")).not.toBeInTheDocument();
  });

  it("calls onSelectKey when leaf clicked", async () => {
    const onSelect = vi.fn();
    render(<EtcdTreePane assetId={1} onSelectKey={onSelect} />);
    await waitFor(() => expect(screen.getByText("version")).toBeInTheDocument());
    fireEvent.click(screen.getByText("version"));
    expect(onSelect).toHaveBeenCalledWith("/version");
  });
});
