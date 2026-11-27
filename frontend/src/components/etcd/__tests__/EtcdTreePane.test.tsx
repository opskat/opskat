import { describe, it, expect, beforeEach, vi } from "vitest";
import { act, render, screen, fireEvent } from "@testing-library/react";
import { EtcdListPrefix } from "../../../../wailsjs/go/etcd/Etcd";
import { EtcdTreePane } from "../EtcdTreePane";
import { useEtcdStore } from "@/stores/etcdStore";
import { getTabConnectionPhase, reconnectTab } from "@/lib/tabReconnect";
import type { Tab } from "@/stores/tabStore";

beforeEach(() => {
  vi.mocked(EtcdListPrefix).mockReset();
  useEtcdStore.setState({
    treeCache: new Map(),
    truncatedAt: new Map(),
    prefixErrors: new Map(),
    loadingPrefixes: new Set(),
    refreshingAssets: new Set(),
  });
  localStorage.clear();
});

describe("EtcdTreePane load states", () => {
  it("shows the failure reason with a retry that reloads the root", async () => {
    vi.mocked(EtcdListPrefix).mockRejectedValueOnce(new Error("context deadline exceeded"));
    render(<EtcdTreePane assetId={1} />);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("context deadline exceeded");

    vi.mocked(EtcdListPrefix).mockResolvedValueOnce({ dirs: ["config"], leaves: [], truncated: false } as never);
    fireEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("config")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows a loading indicator, not an ellipsis, while the root loads", () => {
    vi.mocked(EtcdListPrefix).mockReturnValue(new Promise(() => {}) as never);
    render(<EtcdTreePane assetId={1} />);
    expect(screen.getByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText("…")).not.toBeInTheDocument();
  });

  it("shows an empty state only after a successful empty load", async () => {
    vi.mocked(EtcdListPrefix).mockResolvedValue({ dirs: [], leaves: [], truncated: false } as never);
    render(<EtcdTreePane assetId={1} />);
    expect(await screen.findByTestId("async-state-empty")).toHaveTextContent("etcd.tree.empty");
  });

  it("shows loading, then the failure reason with a retry under a folder whose children fail to load", async () => {
    vi.mocked(EtcdListPrefix).mockResolvedValueOnce({ dirs: ["config"], leaves: [], truncated: false } as never);
    render(<EtcdTreePane assetId={1} />);
    const folder = await screen.findByText("config");

    let rejectChild: (e: Error) => void = () => {};
    vi.mocked(EtcdListPrefix).mockReturnValueOnce(
      new Promise((_, reject) => {
        rejectChild = reject;
      }) as never
    );
    fireEvent.click(folder);
    expect(screen.getByTestId("async-state-loading")).toBeInTheDocument();

    await act(async () => rejectChild(new Error("permission denied on /config/")));
    expect(await screen.findByRole("alert")).toHaveTextContent("permission denied on /config/");

    vi.mocked(EtcdListPrefix).mockResolvedValueOnce({
      dirs: [],
      leaves: [{ key: "/config/db", value: "" }],
      truncated: false,
    } as never);
    fireEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("db")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows a truncation note for a truncated root listing", async () => {
    vi.mocked(EtcdListPrefix).mockResolvedValue({ dirs: ["a"], leaves: [], truncated: true } as never);
    render(<EtcdTreePane assetId={1} />);
    expect(await screen.findByTestId("etcd-tree-truncated")).toHaveTextContent("etcd.tree.truncated");
  });

  it("keeps cached root usable after a failed forced reconnect and lets Retry force-reload", async () => {
    const tab: Tab = {
      id: "query-1",
      type: "query",
      label: "etcd",
      meta: { type: "query", assetId: 1, assetName: "etcd", assetIcon: "", assetType: "etcd" },
    };

    vi.mocked(EtcdListPrefix).mockResolvedValueOnce({ dirs: ["config"], leaves: [], truncated: false } as never);
    render(<EtcdTreePane assetId={1} />);
    expect(await screen.findByText("config")).toBeInTheDocument();
    expect(getTabConnectionPhase(tab)).toBe("connected");

    // Explicit reconnect fails while cached root still exists -> inline error shown, tree stays usable, phase stays connected
    vi.mocked(EtcdListPrefix).mockRejectedValueOnce(new Error("transient timeout"));
    await act(async () => {
      await reconnectTab(tab);
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("transient timeout");
    expect(screen.getByText("config")).toBeInTheDocument();
    expect(getTabConnectionPhase(tab)).toBe("connected");

    // Clicking Retry forces a reload (bypasses treeCache) and clears the error banner
    vi.mocked(EtcdListPrefix).mockResolvedValueOnce({
      dirs: ["config", "services"],
      leaves: [],
      truncated: false,
    } as never);
    fireEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("services")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
