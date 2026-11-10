import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { useOssFileDrop } from "../useOssFileDrop";
import { registerFileDropTarget } from "@/components/terminal/terminalFileDropCoordinator";
import { useOssTransferStore } from "@/stores/ossTransferStore";

vi.mock("@/components/terminal/terminalFileDropCoordinator", () => ({
  registerFileDropTarget: vi.fn(() => vi.fn()),
}));

const resolveKeys = vi.fn();

function Harness() {
  const ref = useRef<HTMLDivElement>(null);
  useOssFileDrop({ dropRef: ref, tabId: "q", assetId: 7, bucket: "b", prefix: "docs/", active: true, resolveKeys });
  return <div ref={ref} />;
}

beforeEach(() => {
  vi.mocked(registerFileDropTarget).mockClear();
  useOssTransferStore.setState({ tabs: {} });
  resolveKeys.mockReset().mockImplementation(async (keys: string[]) => keys);
});

describe("useOssFileDrop", () => {
  it("registers a drop target whose onDrop starts one upload per dropped path", async () => {
    const startUploadPath = vi.fn().mockResolvedValue(undefined);
    useOssTransferStore.setState({ startUploadPath } as never);
    render(<Harness />);
    expect(registerFileDropTarget).toHaveBeenCalledTimes(1);
    const target = vi.mocked(registerFileDropTarget).mock.calls[0][0];
    target.onDrop(["/a/one.txt", "/b/two.txt"]);
    await vi.waitFor(() => expect(startUploadPath).toHaveBeenCalledTimes(2));
    expect(resolveKeys).toHaveBeenCalledWith(["docs/one.txt", "docs/two.txt"]);
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/a/one.txt", "docs/one.txt");
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/b/two.txt", "docs/two.txt");
  });

  it("uploads to the resolved key and skips the dropped paths the resolver rejected", async () => {
    const startUploadPath = vi.fn().mockResolvedValue(undefined);
    useOssTransferStore.setState({ startUploadPath } as never);
    resolveKeys.mockResolvedValue([null, "docs/two_copy.txt"]);
    render(<Harness />);
    vi.mocked(registerFileDropTarget).mock.calls[0][0].onDrop(["/a/one.txt", "/b/two.txt"]);
    await vi.waitFor(() => expect(startUploadPath).toHaveBeenCalledTimes(1));
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/b/two.txt", "docs/two_copy.txt");
  });

  it("does not register when inactive", () => {
    function Inactive() {
      const ref = useRef<HTMLDivElement>(null);
      useOssFileDrop({ dropRef: ref, tabId: "q", assetId: 7, bucket: "b", prefix: "", active: false, resolveKeys });
      return <div ref={ref} />;
    }
    render(<Inactive />);
    expect(registerFileDropTarget).not.toHaveBeenCalled();
  });
});
