import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createRef } from "react";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
import { useNativeFileDrop } from "../components/terminal/file-manager/useNativeFileDrop";
import { resetTerminalFileDropCoordinatorForTest } from "../components/terminal/terminalFileDropCoordinator";

describe("useNativeFileDrop", () => {
  beforeEach(() => {
    resetTerminalFileDropCoordinatorForTest();
    vi.mocked(OnFileDrop).mockClear();
    vi.mocked(OnFileDropOff).mockClear();
  });

  it("hands one drop's paths over as a single batch with the current directory", async () => {
    const panel = document.createElement("div");
    panel.getBoundingClientRect = () =>
      ({ left: 100, right: 300, top: 10, bottom: 500, width: 200, height: 490 }) as DOMRect;
    const panelRef = createRef<HTMLDivElement>();
    Object.defineProperty(panelRef, "current", { value: panel });
    const currentPathRef = { current: "/home/app" };
    const uploadLocalPaths = vi.fn().mockResolvedValue(undefined);

    renderHook(() =>
      useNativeFileDrop({
        currentPathRef,
        isActive: true,
        isOpen: true,
        panelRef,
        uploadLocalPaths,
      })
    );

    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    act(() => handler(150, 20, ["C:/tmp/a.txt", "C:/tmp/b.txt"]));
    await act(async () => {});

    expect(uploadLocalPaths).toHaveBeenCalledTimes(1);
    expect(uploadLocalPaths).toHaveBeenCalledWith(["C:/tmp/a.txt", "C:/tmp/b.txt"], "/home/app");
  });

  it("ignores drops outside the file manager panel", async () => {
    const panel = document.createElement("div");
    panel.getBoundingClientRect = () =>
      ({ left: 100, right: 300, top: 10, bottom: 500, width: 200, height: 490 }) as DOMRect;
    const panelRef = createRef<HTMLDivElement>();
    Object.defineProperty(panelRef, "current", { value: panel });
    const uploadLocalPaths = vi.fn().mockResolvedValue(undefined);

    renderHook(() =>
      useNativeFileDrop({
        currentPathRef: { current: "/home/app" },
        isActive: true,
        isOpen: true,
        panelRef,
        uploadLocalPaths,
      })
    );

    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    act(() => handler(50, 20, ["C:/tmp/a.txt"]));
    await act(async () => {});

    expect(uploadLocalPaths).not.toHaveBeenCalled();
  });
});
