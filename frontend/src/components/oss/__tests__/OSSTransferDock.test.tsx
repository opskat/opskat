import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { OSSTransferDock } from "../OSSTransferDock";
import { useOssTransferStore, type OssTransfer } from "@/stores/ossTransferStore";

function tx(over: Partial<OssTransfer>): OssTransfer {
  return {
    transferId: "t1",
    tabId: "q",
    direction: "upload",
    name: "a.txt",
    bytesDone: 50,
    bytesTotal: 100,
    speed: 1024,
    status: "active",
    retry: { kind: "upload", assetId: 1, bucket: "b", prefix: "", localPath: "/x/a.txt", key: "a.txt" },
    ...over,
  };
}

describe("OSSTransferDock", () => {
  it("renders a row with name, speed and percentage; the button cancels an active row", () => {
    const onCancel = vi.fn();
    const onClear = vi.fn();
    render(<OSSTransferDock transfers={[tx({})]} onCancel={onCancel} onClear={onClear} onClearCompleted={vi.fn()} />);
    expect(screen.getByText("a.txt")).toBeInTheDocument();
    expect(screen.getByText("1.0 KB/s")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("oss-transfer-action-t1"));
    expect(onCancel).toHaveBeenCalledWith("t1");
    expect(onClear).not.toHaveBeenCalled();
  });

  it("shows how many files of a folder download are done", () => {
    render(
      <OSSTransferDock
        transfers={[tx({ direction: "download", name: "logs", files: { done: 3, total: 120 } })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-transfer-files-t1")).toHaveTextContent("3/120");
  });

  it("the row button clears a finished row instead of cancelling", () => {
    const onCancel = vi.fn();
    const onClear = vi.fn();
    render(
      <OSSTransferDock
        transfers={[tx({ transferId: "t2", status: "done" })]}
        onCancel={onCancel}
        onClear={onClear}
        onClearCompleted={vi.fn()}
      />
    );
    fireEvent.click(screen.getByTestId("oss-transfer-action-t2"));
    expect(onClear).toHaveBeenCalledWith("t2");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("clear-completed header button fires onClearCompleted", () => {
    const onClearCompleted = vi.fn();
    render(
      <OSSTransferDock
        transfers={[tx({ status: "done" })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={onClearCompleted}
      />
    );
    fireEvent.click(screen.getByTestId("oss-transfer-clear-completed"));
    expect(onClearCompleted).toHaveBeenCalledTimes(1);
  });

  it("exposes transfer progress and icon actions to assistive technology", () => {
    render(
      <OSSTransferDock
        transfers={[tx({ bytesDone: 25, bytesTotal: 100 })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
    expect(screen.getByTestId("oss-transfer-action-t1")).toHaveAttribute("aria-label", "oss.transfer.cancel");
  });

  it("disables clear completed when every transfer is still active", () => {
    const { rerender } = render(
      <OSSTransferDock transfers={[tx({})]} onCancel={vi.fn()} onClear={vi.fn()} onClearCompleted={vi.fn()} />
    );
    expect(screen.getByTestId("oss-transfer-clear-completed")).toBeDisabled();
    rerender(
      <OSSTransferDock
        transfers={[tx({ status: "done" })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-transfer-clear-completed")).toBeEnabled();
  });

  const renderOne = (over: Partial<OssTransfer>) =>
    render(<OSSTransferDock transfers={[tx(over)]} onCancel={vi.fn()} onClear={vi.fn()} onClearCompleted={vi.fn()} />);

  it("labels every state with text, not just an icon colour", () => {
    for (const [status, key] of [
      ["active", "sftp.transferring"],
      ["done", "sftp.completed"],
      ["error", "sftp.failed"],
      ["cancelled", "sftp.cancelled"],
    ] as const) {
      const { unmount } = renderOne({ status });
      expect(screen.getByTestId("oss-transfer-state-t1")).toHaveTextContent(key);
      unmount();
    }
  });

  it("shows transferred/total size and speed while active", () => {
    renderOne({ bytesDone: 2048, bytesTotal: 4096, speed: 1024 });
    expect(screen.getByText("2.0 KB/4.0 KB")).toBeInTheDocument();
    expect(screen.getByText("1.0 KB/s")).toBeInTheDocument();
  });

  it("shows the failure reason in the row and retries through the store", () => {
    const retry = vi.fn().mockResolvedValue(undefined);
    useOssTransferStore.setState({ retry });
    renderOne({ status: "error", error: "AccessDenied: no permission" });
    expect(screen.getByText("AccessDenied: no permission")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("oss-transfer-retry-t1"));
    expect(retry).toHaveBeenCalledWith("q", "t1");
  });

  it("announces a cancelled row as cancelled only", () => {
    render(
      <OSSTransferDock
        transfers={[tx({ status: "cancelled" })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-transfer-row-t1")).not.toHaveTextContent(/failed/i);
  });

  it("reports a retry that cannot be started", async () => {
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => "");
    useOssTransferStore.setState({ retry: vi.fn().mockRejectedValue(new Error("bucket gone")) });
    render(
      <OSSTransferDock
        transfers={[tx({ status: "error", error: "boom" })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    fireEvent.click(screen.getByTestId("oss-transfer-retry-t1"));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("bucket gone"));
    toastError.mockRestore();
  });

  it("offers retry for cancelled rows but not for done or active ones", () => {
    const { unmount } = renderOne({ status: "cancelled" });
    expect(screen.getByTestId("oss-transfer-retry-t1")).toBeInTheDocument();
    unmount();
    for (const status of ["done", "active"] as const) {
      const r = renderOne({ status });
      expect(screen.queryByTestId("oss-transfer-retry-t1")).toBeNull();
      r.unmount();
    }
  });
});
