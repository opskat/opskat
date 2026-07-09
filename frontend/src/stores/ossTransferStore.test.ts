import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { OSSUploadObject, OSSDownloadObject, OSSCancelTransfer } from "../../wailsjs/go/oss/OSS";
import { useOssTransferStore } from "./ossTransferStore";
import { useOssBrowserStore } from "./ossBrowserStore";

const TAB = "query-9";

// Grab the progress handler that subscribeProgress registered for a given transferId.
function handlerFor(transferId: string): (e: unknown) => void {
  const call = vi.mocked(EventsOn).mock.calls.find((c) => c[0] === "transfer:progress:" + transferId);
  if (!call) throw new Error("no EventsOn for " + transferId);
  return call[1] as (e: unknown) => void;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(EventsOn).mockClear();
  vi.mocked(EventsOff).mockClear();
  vi.mocked(OSSUploadObject).mockReset();
  vi.mocked(OSSDownloadObject).mockReset();
  vi.mocked(OSSCancelTransfer).mockReset().mockResolvedValue(undefined);
  useOssTransferStore.setState({ tabs: {} });
  useOssBrowserStore.setState({ tabs: {} });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("ossTransferStore", () => {
  it("startUpload subscribes one active row per returned transferId", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1", "oss-2"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    const rows = useOssTransferStore.getState().tabs[TAB].transfers;
    expect(Object.keys(rows)).toEqual(["oss-1", "oss-2"]);
    expect(rows["oss-1"].status).toBe("active");
    expect(rows["oss-1"].direction).toBe("upload");
    expect(rows["oss-1"].targetPrefix).toBe("docs/");
    expect(EventsOn).toHaveBeenCalledWith("transfer:progress:oss-1", expect.any(Function));
  });

  it("startUpload with an empty array (dialog cancel) adds no rows", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue([] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    expect(useOssTransferStore.getState().tabs[TAB]?.transfers ?? {}).toEqual({});
  });

  it("a progress event merges numeric fields and keeps status active", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "progress",
      currentFile: "/x/a.txt",
      bytesDone: 50,
      bytesTotal: 100,
      speed: 25,
      filesCompleted: 0,
      filesTotal: 1,
    });
    const row = useOssTransferStore.getState().tabs[TAB].transfers["oss-1"];
    expect(row.status).toBe("active");
    expect(row.bytesDone).toBe(50);
    expect(row.speed).toBe(25);
    expect(row.name).toBe("a.txt");
  });

  it("a done event marks done, EventsOff, and auto-removes after 5s", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "done",
      currentFile: "",
      bytesDone: 100,
      bytesTotal: 100,
      speed: 0,
      filesCompleted: 1,
      filesTotal: 1,
    });
    expect(useOssTransferStore.getState().tabs[TAB].transfers["oss-1"].status).toBe("done");
    expect(EventsOff).toHaveBeenCalledWith("transfer:progress:oss-1");
    vi.advanceTimersByTime(5000);
    expect(useOssTransferStore.getState().tabs[TAB].transfers["oss-1"]).toBeUndefined();
  });

  it("on upload done, refreshes the browser only when targetPrefix === currentPrefix", async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    useOssBrowserStore.setState({ tabs: { [TAB]: { currentPrefix: "docs/" } as never }, refresh } as never);
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "done",
      currentFile: "",
      bytesDone: 1,
      bytesTotal: 1,
      speed: 0,
      filesCompleted: 1,
      filesTotal: 1,
    });
    expect(refresh).toHaveBeenCalledWith(TAB);
  });

  it("on upload done, does NOT refresh when the user navigated away", async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    useOssBrowserStore.setState({ tabs: { [TAB]: { currentPrefix: "other/" } as never }, refresh } as never);
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "done",
      currentFile: "",
      bytesDone: 1,
      bytesTotal: 1,
      speed: 0,
      filesCompleted: 1,
      filesTotal: 1,
    });
    expect(refresh).not.toHaveBeenCalled();
  });

  it("an explicit cancelled event sets status cancelled (no substring inference)", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "cancelled",
      currentFile: "",
      bytesDone: 0,
      bytesTotal: 0,
      speed: 0,
      filesCompleted: 0,
      filesTotal: 0,
    });
    expect(useOssTransferStore.getState().tabs[TAB].transfers["oss-1"].status).toBe("cancelled");
    expect(EventsOff).toHaveBeenCalledWith("transfer:progress:oss-1");
  });

  it("an error event sets status error with the message", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "error",
      error: "boom",
      currentFile: "",
      bytesDone: 0,
      bytesTotal: 0,
      speed: 0,
      filesCompleted: 0,
      filesTotal: 0,
    });
    const row = useOssTransferStore.getState().tabs[TAB].transfers["oss-1"];
    expect(row.status).toBe("error");
    expect(row.error).toBe("boom");
  });

  it("startDownload with an empty id (dialog cancel) adds no rows", async () => {
    vi.mocked(OSSDownloadObject).mockResolvedValue("" as never);
    await useOssTransferStore.getState().startDownload(TAB, 9, "b", "docs/a.txt");
    expect(useOssTransferStore.getState().tabs[TAB]?.transfers ?? {}).toEqual({});
  });

  it("startDownload with an id adds a download row named after the key basename", async () => {
    vi.mocked(OSSDownloadObject).mockResolvedValue("oss-d1" as never);
    await useOssTransferStore.getState().startDownload(TAB, 9, "b", "docs/a.txt");
    const row = useOssTransferStore.getState().tabs[TAB].transfers["oss-d1"];
    expect(row.direction).toBe("download");
    expect(row.name).toBe("a.txt");
  });

  it("cancel calls OSSCancelTransfer", () => {
    useOssTransferStore.getState().cancel("oss-1");
    expect(OSSCancelTransfer).toHaveBeenCalledWith("oss-1");
  });

  it("clearCompleted keeps only active rows", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-1", "oss-2"] as never);
    await useOssTransferStore.getState().startUpload(TAB, 9, "b", "docs/");
    handlerFor("oss-1")({
      transferId: "oss-1",
      status: "error",
      error: "x",
      currentFile: "",
      bytesDone: 0,
      bytesTotal: 0,
      speed: 0,
      filesCompleted: 0,
      filesTotal: 0,
    });
    useOssTransferStore.getState().clearCompleted(TAB);
    const ids = Object.keys(useOssTransferStore.getState().tabs[TAB].transfers);
    expect(ids).toEqual(["oss-2"]);
  });

  it("keeps two tabs isolated", async () => {
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-a"] as never);
    await useOssTransferStore.getState().startUpload("tab-A", 1, "b", "");
    vi.mocked(OSSUploadObject).mockResolvedValue(["oss-b"] as never);
    await useOssTransferStore.getState().startUpload("tab-B", 2, "b", "");
    expect(Object.keys(useOssTransferStore.getState().tabs["tab-A"].transfers)).toEqual(["oss-a"]);
    expect(Object.keys(useOssTransferStore.getState().tabs["tab-B"].transfers)).toEqual(["oss-b"]);
  });
});
