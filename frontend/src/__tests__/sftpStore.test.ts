import { describe, it, expect, beforeEach, vi } from "vitest";
import { useSFTPStore, type SFTPTransfer } from "../stores/sftpStore";
import { SFTPUploadFile } from "../../wailsjs/go/ssh/SSH";
import { SFTPDownload, SFTPDownloadDir, SFTPCancelTransfer } from "../../wailsjs/go/ssh/SSH";
import { EventsOn } from "../../wailsjs/runtime/runtime";

function emit(transferId: string, event: Record<string, unknown>) {
  const call = vi
    .mocked(EventsOn)
    .mock.calls.filter((c) => c[0] === "transfer:progress:" + transferId)
    .at(-1);
  if (!call) throw new Error("no EventsOn for " + transferId);
  (call[1] as (e: unknown) => void)({ transferId, ...event });
}

function makeTransfer(
  id: string,
  sessionId: string,
  status: SFTPTransfer["status"] = "active",
  tabId = `tab-${sessionId}`
): SFTPTransfer {
  return {
    transferId: id,
    tabId,
    sessionId,
    direction: "upload",
    currentFile: "test.txt",
    filesCompleted: 0,
    filesTotal: 1,
    bytesDone: 0,
    bytesTotal: 100,
    speed: 0,
    status,
  };
}

describe("sftpStore", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useSFTPStore.setState({
      transfers: {},
      fileManagerOpenTabs: {},
      fileManagerPaths: {},
      fileManagerWidth: 280,
    });
  });

  describe("clearTransfer", () => {
    it("removes a specific transfer", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1"),
          t2: makeTransfer("t2", "s1"),
        },
      });

      useSFTPStore.getState().clearTransfer("t1");

      const transfers = useSFTPStore.getState().transfers;
      expect(transfers).not.toHaveProperty("t1");
      expect(transfers).toHaveProperty("t2");
    });
  });

  describe("clearCompleted", () => {
    it("removes all non-active transfers", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1", "active"),
          t2: makeTransfer("t2", "s1", "done"),
          t3: makeTransfer("t3", "s1", "error"),
        },
      });

      useSFTPStore.getState().clearCompleted();

      const transfers = useSFTPStore.getState().transfers;
      expect(Object.keys(transfers)).toEqual(["t1"]);
    });

    it("keeps all active transfers", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1", "active"),
          t2: makeTransfer("t2", "s2", "active"),
        },
      });

      useSFTPStore.getState().clearCompleted();
      expect(Object.keys(useSFTPStore.getState().transfers)).toHaveLength(2);
    });
  });

  describe("clearCompletedForSession", () => {
    it("removes non-active transfers for a specific session", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1", "done"),
          t2: makeTransfer("t2", "s1", "active"),
          t3: makeTransfer("t3", "s2", "done"),
        },
      });

      useSFTPStore.getState().clearCompletedForSession("s1");

      const transfers = useSFTPStore.getState().transfers;
      expect(transfers).not.toHaveProperty("t1");
      expect(transfers).toHaveProperty("t2");
      expect(transfers).toHaveProperty("t3");
    });
  });

  describe("clearCompletedForTab", () => {
    it("removes non-active transfers for a specific tab only", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1", "done", "tab1"),
          t2: makeTransfer("t2", "s1", "active", "tab1"),
          t3: makeTransfer("t3", "s2", "done", "tab2"),
        },
      });

      useSFTPStore.getState().clearCompletedForTab("tab1");

      const transfers = useSFTPStore.getState().transfers;
      expect(transfers).not.toHaveProperty("t1");
      expect(transfers).toHaveProperty("t2");
      expect(transfers).toHaveProperty("t3");
    });
  });

  describe("getSessionTransfers", () => {
    it("returns only transfers for the given session", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1"),
          t2: makeTransfer("t2", "s2"),
          t3: makeTransfer("t3", "s1"),
        },
      });

      const result = useSFTPStore.getState().getSessionTransfers("s1");
      expect(result).toHaveLength(2);
      expect(result.map((t) => t.transferId).sort()).toEqual(["t1", "t3"]);
    });

    it("returns empty array when no transfers for session", () => {
      const result = useSFTPStore.getState().getSessionTransfers("unknown");
      expect(result).toEqual([]);
    });
  });

  describe("getTabTransfers", () => {
    it("returns only transfers for the given tab", () => {
      useSFTPStore.setState({
        transfers: {
          t1: makeTransfer("t1", "s1", "active", "tab1"),
          t2: makeTransfer("t2", "s2", "active", "tab2"),
          t3: makeTransfer("t3", "s3", "active", "tab1"),
        },
      });

      const result = useSFTPStore.getState().getTabTransfers("tab1");
      expect(result).toHaveLength(2);
      expect(result.map((t) => t.transferId).sort()).toEqual(["t1", "t3"]);
    });
  });

  describe("toggleFileManager", () => {
    it("toggles file manager open state for a tab", () => {
      useSFTPStore.getState().toggleFileManager("tab1");
      expect(useSFTPStore.getState().fileManagerOpenTabs["tab1"]).toBe(true);

      useSFTPStore.getState().toggleFileManager("tab1");
      expect(useSFTPStore.getState().fileManagerOpenTabs["tab1"]).toBe(false);
    });
  });

  describe("setFileManagerPath", () => {
    it("stores current file manager path per tab", () => {
      useSFTPStore.getState().setFileManagerPath("tab1", "/var/log");
      useSFTPStore.getState().setFileManagerPath("tab2", "/srv/app");

      expect(useSFTPStore.getState().fileManagerPaths["tab1"]).toBe("/var/log");
      expect(useSFTPStore.getState().fileManagerPaths["tab2"]).toBe("/srv/app");
    });
  });

  describe("setFileManagerWidth", () => {
    it("sets width within bounds", () => {
      useSFTPStore.getState().setFileManagerWidth(400);
      expect(useSFTPStore.getState().fileManagerWidth).toBe(400);
    });

    it("clamps to minimum 200", () => {
      useSFTPStore.getState().setFileManagerWidth(50);
      expect(useSFTPStore.getState().fileManagerWidth).toBe(200);
    });

    it("clamps to maximum 600", () => {
      useSFTPStore.getState().setFileManagerWidth(1000);
      expect(useSFTPStore.getState().fileManagerWidth).toBe(600);
    });
  });

  describe("cancelTransfer", () => {
    it("calls backend SFTPCancelTransfer", () => {
      useSFTPStore.getState().cancelTransfer("t1");
      expect(SFTPCancelTransfer).toHaveBeenCalledWith("t1");
    });
  });

  describe("startUploadFile", () => {
    it("calls backend and initializes transfer on success", async () => {
      vi.mocked(SFTPUploadFile).mockResolvedValue("transfer-123");

      const result = await useSFTPStore
        .getState()
        .startUploadFile({ tabId: "tab1", sessionId: "s1" }, "/local/path", "/remote/path");
      expect(result).toBe("transfer-123");
      expect(SFTPUploadFile).toHaveBeenCalledWith("s1", "/local/path", "/remote/path");
      expect(useSFTPStore.getState().transfers["transfer-123"]).toBeDefined();
      expect(useSFTPStore.getState().transfers["transfer-123"].tabId).toBe("tab1");
      expect(useSFTPStore.getState().transfers["transfer-123"].sessionId).toBe("s1");
      expect(useSFTPStore.getState().transfers["transfer-123"].status).toBe("active");
      expect(useSFTPStore.getState().transfers["transfer-123"].direction).toBe("upload");
    });

    it("returns null when backend returns empty", async () => {
      vi.mocked(SFTPUploadFile).mockResolvedValue("");

      const result = await useSFTPStore
        .getState()
        .startUploadFile({ tabId: "tab1", sessionId: "s1" }, "/local/path", "/remote/path");
      expect(result).toBeNull();
    });

    it("keeps transfers scoped to their initiating tabs", async () => {
      vi.mocked(SFTPUploadFile).mockResolvedValueOnce("transfer-1").mockResolvedValueOnce("transfer-2");

      await useSFTPStore
        .getState()
        .startUploadFile({ tabId: "tab1", sessionId: "s1" }, "/local/file1", "/remote/file1");
      await useSFTPStore
        .getState()
        .startUploadFile({ tabId: "tab2", sessionId: "s2" }, "/local/file2", "/remote/file2");

      expect(
        useSFTPStore
          .getState()
          .getTabTransfers("tab1")
          .map((transfer) => transfer.transferId)
      ).toEqual(["transfer-1"]);
      expect(
        useSFTPStore
          .getState()
          .getTabTransfers("tab2")
          .map((transfer) => transfer.transferId)
      ).toEqual(["transfer-2"]);
      expect(SFTPUploadFile).toHaveBeenNthCalledWith(1, "s1", "/local/file1", "/remote/file1");
      expect(SFTPUploadFile).toHaveBeenNthCalledWith(2, "s2", "/local/file2", "/remote/file2");
    });
  });

  describe("startDownload", () => {
    it("calls backend and initializes download transfer", async () => {
      vi.mocked(SFTPDownload).mockResolvedValue("dl-123");

      const result = await useSFTPStore
        .getState()
        .startDownload({ tabId: "tab1", sessionId: "s1" }, "/remote/file.txt");
      expect(result).toBe("dl-123");
      expect(useSFTPStore.getState().transfers["dl-123"].tabId).toBe("tab1");
      expect(useSFTPStore.getState().transfers["dl-123"].direction).toBe("download");
    });
  });

  describe("finished rows", () => {
    const target = { tabId: "tab1", sessionId: "s1" };

    it("keeps a done row until the user clears it", async () => {
      vi.useFakeTimers();
      try {
        vi.mocked(SFTPUploadFile).mockResolvedValue("u1");
        await useSFTPStore.getState().startUploadFile(target, "/l/a.txt", "/r/a.txt");
        emit("u1", { status: "done" });
        vi.advanceTimersByTime(60_000);
        expect(useSFTPStore.getState().transfers["u1"].status).toBe("done");
      } finally {
        vi.useRealTimers();
      }
    });

    it("an explicit cancelled event is cancelled; any error stays failed with its reason", async () => {
      vi.mocked(SFTPUploadFile).mockResolvedValueOnce("u1").mockResolvedValueOnce("u2");
      await useSFTPStore.getState().startUploadFile(target, "/l/a.txt", "/r/a.txt");
      await useSFTPStore.getState().startUploadFile(target, "/l/b.txt", "/r/b.txt");
      emit("u1", { status: "cancelled" });
      emit("u2", { status: "error", error: "wrapped: context canceled by peer" });
      expect(useSFTPStore.getState().transfers["u1"].status).toBe("cancelled");
      expect(useSFTPStore.getState().transfers["u2"].status).toBe("error");
      expect(useSFTPStore.getState().transfers["u2"].error).toBe("wrapped: context canceled by peer");
    });

    it("records the local path a download finished at", async () => {
      vi.mocked(SFTPDownload).mockResolvedValue("d1");
      await useSFTPStore.getState().startDownload(target, "/r/a.txt");
      emit("d1", { status: "done", localPath: "/Users/me/a.txt" });
      expect(useSFTPStore.getState().transfers["d1"].localPath).toBe("/Users/me/a.txt");
    });
  });

  describe("retryTransfer", () => {
    const target = { tabId: "tab1", sessionId: "s1" };
    beforeEach(() => {
      vi.mocked(SFTPUploadFile).mockReset();
      vi.mocked(SFTPDownload).mockReset();
      vi.mocked(SFTPDownloadDir).mockReset();
    });

    it("re-runs a failed upload with the same local and remote paths and replaces the row", async () => {
      vi.mocked(SFTPUploadFile).mockResolvedValueOnce("u1").mockResolvedValueOnce("u2");
      await useSFTPStore.getState().startUploadFile(target, "/l/a.txt", "/r/a.txt");
      emit("u1", { status: "error", error: "boom" });

      await useSFTPStore.getState().retryTransfer("u1");

      expect(SFTPUploadFile).toHaveBeenLastCalledWith("s1", "/l/a.txt", "/r/a.txt");
      const ids = Object.keys(useSFTPStore.getState().transfers);
      expect(ids).toEqual(["u2"]);
      expect(useSFTPStore.getState().transfers["u2"].status).toBe("active");
    });

    it("re-runs a failed download and a failed directory download", async () => {
      vi.mocked(SFTPDownload).mockResolvedValueOnce("d1").mockResolvedValueOnce("d2");
      vi.mocked(SFTPDownloadDir).mockResolvedValueOnce("g1").mockResolvedValueOnce("g2");
      await useSFTPStore.getState().startDownload(target, "/r/a.txt");
      await useSFTPStore.getState().startDownloadDir(target, "/r/dir");
      emit("d1", { status: "error", error: "x" });
      emit("g1", { status: "cancelled" });

      await useSFTPStore.getState().retryTransfer("d1");
      await useSFTPStore.getState().retryTransfer("g1");

      expect(SFTPDownload).toHaveBeenLastCalledWith("s1", "/r/a.txt");
      expect(SFTPDownloadDir).toHaveBeenLastCalledWith("s1", "/r/dir");
      expect(Object.keys(useSFTPStore.getState().transfers).sort()).toEqual(["d2", "g2"]);
    });

    it("keeps the failed row when the retry dialog is dismissed", async () => {
      vi.mocked(SFTPDownload).mockResolvedValueOnce("d1").mockResolvedValueOnce("");
      await useSFTPStore.getState().startDownload(target, "/r/a.txt");
      emit("d1", { status: "error", error: "x" });
      await useSFTPStore.getState().retryTransfer("d1");
      expect(Object.keys(useSFTPStore.getState().transfers)).toEqual(["d1"]);
    });
  });
});
