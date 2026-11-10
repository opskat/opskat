import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TransferSection } from "../TransferSection";
import { useSFTPStore, type SFTPTransfer } from "@/stores/sftpStore";
import { OpenDirectory } from "../../../../../wailsjs/go/system/System";

function tx(over: Partial<SFTPTransfer>): SFTPTransfer {
  return {
    transferId: "t1",
    tabId: "tab",
    sessionId: "s",
    direction: "upload",
    currentFile: "/remote/a.txt",
    filesCompleted: 0,
    filesTotal: 1,
    bytesDone: 2048,
    bytesTotal: 4096,
    speed: 1024,
    status: "active",
    retry: { kind: "upload", localPath: "/l/a.txt", remotePath: "/remote/a.txt" },
    ...over,
  };
}

const renderOne = (over: Partial<SFTPTransfer>) => render(<TransferSection tabId="tab" transfers={[tx(over)]} />);

describe("TransferSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useSFTPStore.setState({ transfers: {} });
  });

  it("labels each state with text", () => {
    for (const [status, key] of [
      ["active", "sftp.transferring"],
      ["done", "sftp.completed"],
      ["error", "sftp.failed"],
      ["cancelled", "sftp.cancelled"],
    ] as const) {
      const { unmount } = renderOne({ status });
      expect(screen.getByTestId("sftp-transfer-state-t1")).toHaveTextContent(key);
      unmount();
    }
  });

  it("shows transferred/total size and speed while active", () => {
    renderOne({});
    expect(screen.getByText("2.0 KB/4.0 KB")).toBeInTheDocument();
    expect(screen.getByText("1.0 KB/s")).toBeInTheDocument();
  });

  it("finished, failed and cancelled rows keep the transferred/total size but drop the speed", () => {
    for (const status of ["done", "error", "cancelled"] as const) {
      const { unmount } = renderOne({ status, error: status === "error" ? "boom" : undefined });
      expect(screen.getByText("2.0 KB/4.0 KB")).toBeInTheDocument();
      expect(screen.queryByText("1.0 KB/s")).toBeNull();
      unmount();
    }
  });

  it("active rows have a text cancel button that cancels the transfer", () => {
    const cancelTransfer = vi.fn();
    useSFTPStore.setState({ cancelTransfer });
    renderOne({});
    fireEvent.click(screen.getByRole("button", { name: "sftp.cancelTransfer" }));
    expect(cancelTransfer).toHaveBeenCalledWith("t1");
  });

  it("shows the failure reason and a retry button for failed rows", () => {
    const retryTransfer = vi.fn().mockResolvedValue(null);
    useSFTPStore.setState({ retryTransfer });
    renderOne({ status: "error", error: "permission denied" });
    expect(screen.getByText("permission denied")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "sftp.retry" }));
    expect(retryTransfer).toHaveBeenCalledWith("t1");
  });

  it("does not offer retry for transfers that cannot be retried", () => {
    renderOne({ status: "cancelled", retry: undefined });
    expect(screen.queryByRole("button", { name: "sftp.retry" })).toBeNull();
  });

  it("a finished download opens the folder containing the file", () => {
    renderOne({
      status: "done",
      direction: "download",
      localPath: "/Users/me/Downloads/a.txt",
      retry: { kind: "download", remotePath: "/remote/a.txt" },
    });
    fireEvent.click(screen.getByRole("button", { name: "sftp.showInFolder" }));
    expect(OpenDirectory).toHaveBeenCalledWith("/Users/me/Downloads");
  });

  it("a finished directory download opens the directory itself", () => {
    renderOne({
      status: "done",
      direction: "download",
      localPath: "C:\\Users\\me\\logs",
      retry: { kind: "downloadDir", remotePath: "/remote/logs" },
    });
    fireEvent.click(screen.getByRole("button", { name: "sftp.showInFolder" }));
    expect(OpenDirectory).toHaveBeenCalledWith("C:\\Users\\me\\logs");
  });

  it("uploads and unfinished downloads have no open-folder action", () => {
    const { unmount } = renderOne({ status: "done" });
    expect(screen.queryByRole("button", { name: "sftp.showInFolder" })).toBeNull();
    unmount();
    renderOne({ direction: "download", localPath: undefined });
    expect(screen.queryByRole("button", { name: "sftp.showInFolder" })).toBeNull();
  });

  it("the header clears finished rows only, via a labelled button", () => {
    const clearCompletedForTab = vi.fn();
    useSFTPStore.setState({ clearCompletedForTab });
    renderOne({ status: "done" });
    fireEvent.click(screen.getByRole("button", { name: "sftp.clearCompleted" }));
    expect(clearCompletedForTab).toHaveBeenCalledWith("tab");
  });
});
