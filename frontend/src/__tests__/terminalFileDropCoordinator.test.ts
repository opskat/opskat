import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
import {
  registerFileManagerDropTarget,
  registerFileDropTarget,
  registerTerminalFileDropTarget,
  resetTerminalFileDropCoordinatorForTest,
} from "../components/terminal/terminalFileDropCoordinator";

function rect(left: number, top: number, right: number, bottom: number): DOMRect {
  return { left, top, right, bottom, width: right - left, height: bottom - top } as DOMRect;
}

describe("terminalFileDropCoordinator", () => {
  beforeEach(() => {
    resetTerminalFileDropCoordinatorForTest();
    vi.mocked(OnFileDrop).mockClear();
    vi.mocked(OnFileDropOff).mockClear();
  });

  afterEach(() => {
    resetTerminalFileDropCoordinatorForTest();
  });

  it("routes terminal drops by pointer coordinates instead of last registered target", () => {
    const leftUpload = vi.fn();
    const rightUpload = vi.fn();

    registerTerminalFileDropTarget({
      getRect: () => rect(0, 0, 200, 400),
      uploadFiles: leftUpload,
    });
    registerTerminalFileDropTarget({
      getRect: () => rect(200, 0, 400, 400),
      uploadFiles: rightUpload,
    });

    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    handler(100, 20, ["/tmp/a.txt"]);

    expect(leftUpload).toHaveBeenCalledWith(["/tmp/a.txt"]);
    expect(rightUpload).not.toHaveBeenCalled();
    expect(OnFileDrop).toHaveBeenCalledTimes(1);
  });

  it("keeps the global listener active until the last target unregisters", () => {
    const unregisterLeft = registerTerminalFileDropTarget({
      getRect: () => rect(0, 0, 200, 400),
      uploadFiles: vi.fn(),
    });
    const unregisterRight = registerTerminalFileDropTarget({
      getRect: () => rect(200, 0, 400, 400),
      uploadFiles: vi.fn(),
    });

    unregisterLeft();
    expect(OnFileDropOff).not.toHaveBeenCalled();

    unregisterRight();
    expect(OnFileDropOff).toHaveBeenCalledTimes(1);
  });

  it("prioritizes file manager SFTP drops and routes panel-outside drops to the hit terminal", () => {
    const terminalUpload = vi.fn();
    const fileUpload = vi.fn();

    registerTerminalFileDropTarget({
      getRect: () => rect(0, 0, 500, 400),
      uploadFiles: terminalUpload,
    });
    registerFileManagerDropTarget({
      getRect: () => rect(300, 0, 500, 400),
      getRemoteDir: () => "/srv/app/",
      startUploadFile: fileUpload,
    });

    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    handler(350, 20, ["/tmp/in-panel.txt"]);
    handler(100, 20, ["/tmp/in-terminal.txt"]);

    expect(fileUpload).toHaveBeenCalledWith("/tmp/in-panel.txt", "/srv/app/");
    expect(terminalUpload).toHaveBeenCalledWith(["/tmp/in-terminal.txt"]);
    expect(terminalUpload).not.toHaveBeenCalledWith(["/tmp/in-panel.txt"]);
  });
});

describe("registerFileDropTarget (generic)", () => {
  beforeEach(() => {
    resetTerminalFileDropCoordinatorForTest();
    vi.mocked(OnFileDrop).mockClear();
    vi.mocked(OnFileDropOff).mockClear();
  });

  afterEach(() => {
    resetTerminalFileDropCoordinatorForTest();
  });

  it("routes a drop within the target's rect to onDrop, and toggles OnFileDrop on/off", () => {
    const onDrop = vi.fn();
    const rect = { left: 0, top: 0, right: 100, bottom: 100 } as DOMRect;
    const unregister = registerFileDropTarget({ getRect: () => rect, onDrop });
    expect(OnFileDrop).toHaveBeenCalledTimes(1);
    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    handler(50, 50, ["/a/b.txt", "/c/d.txt"]);
    expect(onDrop).toHaveBeenCalledWith(["/a/b.txt", "/c/d.txt"]);
    handler(500, 500, ["/x.txt"]); // outside the rect
    expect(onDrop).toHaveBeenCalledTimes(1);
    unregister();
    expect(OnFileDropOff).toHaveBeenCalled();
  });
});
