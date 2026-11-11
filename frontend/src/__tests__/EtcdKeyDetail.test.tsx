import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { EtcdKeyDetail } from "@/components/etcd/EtcdKeyDetail";
import { useEtcdStore } from "@/stores/etcdStore";
import type { etcd_svc } from "../../wailsjs/go/models";

vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

const kv = {
  key: "/app/config",
  value: '{"a":1}',
  modRevision: 7,
  createRevision: 3,
  version: 2,
  lease: 0,
} as unknown as etcd_svc.EtcdKV;

function result(kvs: etcd_svc.EtcdKV[]): etcd_svc.ExecResult {
  return { op: "get", count: kvs.length, kvs, revision: 7 } as unknown as etcd_svc.ExecResult;
}

describe("EtcdKeyDetail", () => {
  const exec = vi.fn<(req: etcd_svc.ExecRequest) => Promise<etcd_svc.ExecResult>>();

  beforeEach(() => {
    exec.mockReset();
    useEtcdStore.setState({ exec });
  });

  it("shows the failure reason with a retry that reloads the key", async () => {
    exec.mockRejectedValueOnce(new Error("context deadline exceeded")).mockResolvedValueOnce(result([kv]));

    render(<EtcdKeyDetail assetId={1} selectedKey="/app/config" />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("context deadline exceeded");
    expect(screen.queryByText("etcd.detail.empty")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "action.retry" }));

    expect(await screen.findByTestId("etcd-key-detail")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(exec).toHaveBeenCalledTimes(2);
  });

  it("shows the empty state only when the key loaded successfully but does not exist", async () => {
    exec.mockResolvedValueOnce(result([]));

    render(<EtcdKeyDetail assetId={1} selectedKey="/gone" />);

    expect(await screen.findByText("etcd.detail.empty")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
