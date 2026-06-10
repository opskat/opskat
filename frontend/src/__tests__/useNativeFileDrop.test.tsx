import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createRef } from "react";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime/runtime";
import { useNativeFileDrop } from "../components/terminal/file-manager/useNativeFileDrop";

describe("useNativeFileDrop", () => {
  beforeEach(() => {
    vi.mocked(OnFileDrop).mockClear();
    vi.mocked(OnFileDropOff).mockClear();
  });

  it("uploads through SFTP when dropped inside the file manager panel", () => {
    const panel = document.createElement("div");
    panel.getBoundingClientRect = () =>
      ({ left: 100, right: 300, top: 10, bottom: 500, width: 200, height: 490 }) as DOMRect;
    const panelRef = createRef<HTMLDivElement>();
    Object.defineProperty(panelRef, "current", { value: panel });
    const currentPathRef = { current: "/home/app" };
    const startUploadFile = vi.fn().mockResolvedValue("t1");
    const onDropOutside = vi.fn();

    renderHook(() =>
      useNativeFileDrop({
        currentPathRef,
        isActive: true,
        isOpen: true,
        panelRef,
        tabId: "tab1",
        sessionId: "s1",
        startUploadFile,
        onDropOutside,
      })
    );

    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    act(() => handler(150, 20, ["C:/tmp/a.txt"]));

    expect(startUploadFile).toHaveBeenCalledWith({ tabId: "tab1", sessionId: "s1" }, "C:/tmp/a.txt", "/home/app/");
    expect(onDropOutside).not.toHaveBeenCalled();
  });

  it("routes drops outside the panel to the terminal rz uploader", () => {
    const panel = document.createElement("div");
    panel.getBoundingClientRect = () =>
      ({ left: 100, right: 300, top: 10, bottom: 500, width: 200, height: 490 }) as DOMRect;
    const panelRef = createRef<HTMLDivElement>();
    Object.defineProperty(panelRef, "current", { value: panel });
    const startUploadFile = vi.fn().mockResolvedValue("t1");
    const onDropOutside = vi.fn();

    renderHook(() =>
      useNativeFileDrop({
        currentPathRef: { current: "/home/app" },
        isActive: true,
        isOpen: true,
        panelRef,
        tabId: "tab1",
        sessionId: "s1",
        startUploadFile,
        onDropOutside,
      })
    );

    const handler = vi.mocked(OnFileDrop).mock.calls[0][0];
    act(() => handler(50, 20, ["C:/tmp/a.txt"]));

    expect(onDropOutside).toHaveBeenCalledWith(["C:/tmp/a.txt"]);
    expect(startUploadFile).not.toHaveBeenCalled();
  });
});
