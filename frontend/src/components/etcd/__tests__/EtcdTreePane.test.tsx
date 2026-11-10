import { describe, it, expect, beforeEach, vi } from "vitest";
import { act, render, screen, fireEvent } from "@testing-library/react";
import { EtcdListPrefix } from "../../../../wailsjs/go/etcd/Etcd";
import { EtcdTreePane } from "../EtcdTreePane";
import { useEtcdStore } from "@/stores/etcdStore";

beforeEach(() => {
  vi.mocked(EtcdListPrefix).mockReset();
  useEtcdStore.setState({ treeCache: new Map(), truncatedAt: new Map() });
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
});
