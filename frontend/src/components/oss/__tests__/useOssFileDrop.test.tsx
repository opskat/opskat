import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { useRef } from "react";
import { useOssFileDrop } from "../useOssFileDrop";
import { registerFileDropTarget } from "@/components/terminal/terminalFileDropCoordinator";
import { useOssTransferStore } from "@/stores/ossTransferStore";

vi.mock("@/components/terminal/terminalFileDropCoordinator", () => ({
  registerFileDropTarget: vi.fn(() => vi.fn()),
}));

function Harness() {
  const ref = useRef<HTMLDivElement>(null);
  useOssFileDrop({ dropRef: ref, tabId: "q", assetId: 7, bucket: "b", prefix: "docs/", active: true });
  return <div ref={ref} />;
}

beforeEach(() => {
  vi.mocked(registerFileDropTarget).mockClear();
  useOssTransferStore.setState({ tabs: {} });
});

describe("useOssFileDrop", () => {
  it("registers a drop target whose onDrop starts one upload per dropped path", () => {
    const startUploadPath = vi.fn().mockResolvedValue(undefined);
    useOssTransferStore.setState({ startUploadPath } as never);
    render(<Harness />);
    expect(registerFileDropTarget).toHaveBeenCalledTimes(1);
    const target = vi.mocked(registerFileDropTarget).mock.calls[0][0];
    target.onDrop(["/a/one.txt", "/b/two.txt"]);
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/a/one.txt");
    expect(startUploadPath).toHaveBeenCalledWith("q", 7, "b", "docs/", "/b/two.txt");
  });

  it("does not register when inactive", () => {
    function Inactive() {
      const ref = useRef<HTMLDivElement>(null);
      useOssFileDrop({ dropRef: ref, tabId: "q", assetId: 7, bucket: "b", prefix: "", active: false });
      return <div ref={ref} />;
    }
    render(<Inactive />);
    expect(registerFileDropTarget).not.toHaveBeenCalled();
  });
});
