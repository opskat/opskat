import { useRef, useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Terminal as XTerminal } from "@xterm/xterm";
import { useTerminalMouseInteraction } from "@/components/terminal/useTerminalMouseInteraction";
import { useShortcutStore, DEFAULT_SHORTCUTS } from "@/stores/shortcutStore";
import { cloneDefaultMouseShortcuts } from "@/stores/terminalMouseShortcuts";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { useTabStore } from "@/stores/tabStore";

function makeFakeTerm(selection = "", mouseTrackingMode: "none" | "vt200" = "none") {
  let currentSelection = selection;
  const term = {
    modes: { mouseTrackingMode },
    getSelection: vi.fn(() => currentSelection),
    clearSelection: vi.fn(() => {
      currentSelection = "";
    }),
    clear: vi.fn(),
    selectAll: vi.fn(),
    focus: vi.fn(),
    setSelection(next: string) {
      currentSelection = next;
    },
  };
  return term;
}

interface HarnessProps {
  term: ReturnType<typeof makeFakeTerm>;
  readOnly?: boolean;
  onCopy?: () => boolean;
  onCopyText?: (text: string) => void;
  onPaste?: () => void;
  onClear?: () => void;
  onFocusPane?: () => void;
}

function Harness({
  term,
  readOnly = false,
  onCopy = vi.fn(() => true),
  onCopyText = vi.fn(),
  onPaste = vi.fn(),
  onClear = vi.fn(),
  onFocusPane = vi.fn(),
}: HarnessProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [popover, setPopover] = useState<{ left: number; top: number } | null>(null);
  const { onMouseDownCapture, onContextMenuCapture, onAuxClickCapture } = useTerminalMouseInteraction({
    wrapperRef,
    getTerm: () => term as unknown as XTerminal,
    readOnly,
    onCopy,
    onCopyText,
    onPaste,
    onClear,
    onFocusPane,
    onFloatingPopoverChange: setPopover,
  });

  return (
    <div>
      <div
        ref={wrapperRef}
        data-testid="surface"
        onMouseDownCapture={onMouseDownCapture}
        onContextMenuCapture={onContextMenuCapture}
        onAuxClickCapture={onAuxClickCapture}
      >
        {popover && <div data-testid="popover">popover</div>}
      </div>
      <div data-testid="outside">outside</div>
    </div>
  );
}

describe("useTerminalMouseInteraction", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      cb(0);
      return 1;
    });
    localStorage.clear();
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      mouseShortcuts: cloneDefaultMouseShortcuts(),
      isRecording: false,
    });
    useTerminalThemeStore.setState({
      selectionAction: "popover",
    });
    useTabStore.setState({
      tabs: [
        { id: "tab-1", type: "page", label: "one", meta: { type: "page", pageId: "settings" } },
        { id: "tab-2", type: "page", label: "two", meta: { type: "page", pageId: "sshkeys" } },
      ],
      activeTabId: "tab-2",
    });
  });

  it("auto-copies on mouseup even when dragging outside the terminal, and clears selection immediately", () => {
    useTerminalThemeStore.setState({ selectionAction: "auto-copy" });
    const term = makeFakeTerm("echo hello");
    const onCopyText = vi.fn();
    render(<Harness term={term} onCopyText={onCopyText} />);

    fireEvent.mouseDown(screen.getByTestId("surface"), { button: 0, clientX: 20, clientY: 20 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 500, clientY: 500, bubbles: true }));
    });

    expect(onCopyText).toHaveBeenCalledTimes(1);
    expect(onCopyText).toHaveBeenCalledWith("echo hello");
    expect(term.clearSelection).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("popover")).not.toBeInTheDocument();
  });

  it("skips whitespace-only selections in auto-copy mode", () => {
    useTerminalThemeStore.setState({ selectionAction: "auto-copy" });
    const term = makeFakeTerm("   \n\t  ");
    const onCopyText = vi.fn();
    render(<Harness term={term} onCopyText={onCopyText} />);

    fireEvent.mouseDown(screen.getByTestId("surface"), { button: 0, clientX: 10, clientY: 10 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 50, clientY: 10, bubbles: true }));
    });

    expect(onCopyText).not.toHaveBeenCalled();
    expect(term.clearSelection).not.toHaveBeenCalled();
  });

  it("shows selection popover only in popover mode, and does nothing extra in none mode", () => {
    const term = makeFakeTerm("selected text");
    const onCopyText = vi.fn();
    render(<Harness term={term} onCopyText={onCopyText} />);

    fireEvent.mouseDown(screen.getByTestId("surface"), { button: 0, clientX: 20, clientY: 20 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 80, clientY: 80, bubbles: true }));
    });
    expect(screen.getByTestId("popover")).toBeInTheDocument();

    act(() => {
      useTerminalThemeStore.setState({ selectionAction: "none" });
    });
    expect(screen.queryByTestId("popover")).not.toBeInTheDocument();

    fireEvent.mouseDown(screen.getByTestId("surface"), { button: 0, clientX: 20, clientY: 20 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 80, clientY: 80, bubbles: true }));
    });
    expect(screen.queryByTestId("popover")).not.toBeInTheDocument();
    expect(onCopyText).not.toHaveBeenCalled();
    expect(term.clearSelection).not.toHaveBeenCalled();
  });

  it("prevents middle-click autoscroll on mousedown and pastes on mouseup (unless dragged > 5px)", () => {
    const term = makeFakeTerm("");
    const onPaste = vi.fn();
    render(<Harness term={term} onPaste={onPaste} />);

    const surface = screen.getByTestId("surface");
    const downEv = new MouseEvent("mousedown", {
      button: 1,
      clientX: 50,
      clientY: 50,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(downEv);
    expect(downEv.defaultPrevented).toBe(true);

    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 52, clientY: 51, bubbles: true }));
    });
    expect(onPaste).toHaveBeenCalledTimes(1);

    // Dragged middle click (> 5px) must NOT fire paste
    onPaste.mockClear();
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 1, clientX: 50, clientY: 50, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 80, clientY: 50, bubbles: true }));
    });
    expect(onPaste).not.toHaveBeenCalled();
  });

  it("prevents WebView history navigation on side buttons (3/4) and dispatches tab.prev / tab.next on mouseup", () => {
    const term = makeFakeTerm("");
    render(<Harness term={term} />);
    const surface = screen.getByTestId("surface");

    const backDown = new MouseEvent("mousedown", {
      button: 3,
      clientX: 40,
      clientY: 40,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(backDown);
    expect(backDown.defaultPrevented).toBe(true);

    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 3, clientX: 40, clientY: 40, bubbles: true }));
    });
    expect(useTabStore.getState().activeTabId).toBe("tab-1");

    const fwdDown = new MouseEvent("mousedown", {
      button: 4,
      clientX: 40,
      clientY: 40,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(fwdDown);
    expect(fwdDown.defaultPrevented).toBe(true);

    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 4, clientX: 40, clientY: 40, bubbles: true }));
    });
    expect(useTabStore.getState().activeTabId).toBe("tab-2");
  });

  it("executes right-click bindings once in contextmenu capture and respects Shift+Right escape hatch", () => {
    useShortcutStore.getState().updateMouseShortcut("right", "terminal.paste");
    const term = makeFakeTerm("");
    const onPaste = vi.fn();
    render(<Harness term={term} onPaste={onPaste} />);
    const surface = screen.getByTestId("surface");

    const ctxEv = new MouseEvent("contextmenu", {
      button: 2,
      clientX: 30,
      clientY: 30,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(ctxEv);
    expect(ctxEv.defaultPrevented).toBe(true);
    expect(onPaste).toHaveBeenCalledTimes(1);

    // Shift+Right opens menu (does NOT preventDefault, does NOT paste)
    onPaste.mockClear();
    const shiftCtxEv = new MouseEvent("contextmenu", {
      button: 2,
      shiftKey: true,
      clientX: 30,
      clientY: 30,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(shiftCtxEv);
    expect(shiftCtxEv.defaultPrevented).toBe(false);
    expect(onPaste).not.toHaveBeenCalled();
  });

  it("respects remote TUI mouse tracking with Shift bypass while keeping side buttons active", () => {
    const term = makeFakeTerm("", "vt200");
    const onPaste = vi.fn();
    render(<Harness term={term} onPaste={onPaste} />);
    const surface = screen.getByTestId("surface");

    // Middle click without Shift passes through to TUI
    const midDown = new MouseEvent("mousedown", {
      button: 1,
      clientX: 20,
      clientY: 20,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(midDown);
    expect(midDown.defaultPrevented).toBe(false);
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 20, clientY: 20, bubbles: true }));
    });
    expect(onPaste).not.toHaveBeenCalled();

    // Shift+Middle bypasses TUI and pastes
    const shiftMidDown = new MouseEvent("mousedown", {
      button: 1,
      shiftKey: true,
      clientX: 20,
      clientY: 20,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(shiftMidDown);
    expect(shiftMidDown.defaultPrevented).toBe(true);
    act(() => {
      window.dispatchEvent(
        new MouseEvent("mouseup", { button: 1, shiftKey: true, clientX: 20, clientY: 20, bubbles: true })
      );
    });
    expect(onPaste).toHaveBeenCalledTimes(1);

    // Side Back still switches tabs even while TUI tracks mouse
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 3, clientX: 20, clientY: 20, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 3, clientX: 20, clientY: 20, bubbles: true }));
    });
    expect(useTabStore.getState().activeTabId).toBe("tab-1");
  });

  it("skips write actions in readOnly mode (K8sLogTerminal) while keeping auto-copy and tab nav", () => {
    useTerminalThemeStore.setState({ selectionAction: "auto-copy" });
    useShortcutStore.getState().updateMouseShortcut("right", "terminal.clear");
    const term = makeFakeTerm("pod log line");
    const onPaste = vi.fn();
    const onClear = vi.fn();
    const onCopyText = vi.fn();
    render(<Harness term={term} readOnly onPaste={onPaste} onClear={onClear} onCopyText={onCopyText} />);
    const surface = screen.getByTestId("surface");

    // Middle click paste is skipped
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 1, clientX: 10, clientY: 10, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 10, clientY: 10, bubbles: true }));
    });
    expect(onPaste).not.toHaveBeenCalled();

    // Right click clear is skipped
    surface.dispatchEvent(new MouseEvent("contextmenu", { button: 2, bubbles: true, cancelable: true }));
    expect(onClear).not.toHaveBeenCalled();

    // Auto-copy still works and clears selection
    fireEvent.mouseDown(surface, { button: 0, clientX: 10, clientY: 10 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 60, clientY: 10, bubbles: true }));
    });
    expect(onCopyText).toHaveBeenCalledWith("pod log line");
    expect(term.clearSelection).toHaveBeenCalledTimes(1);
  });

  it("refocuses terminal for terminal-local actions (paste/clear) but not for workspace actions", () => {
    const term = makeFakeTerm("");
    const onPaste = vi.fn();
    const onClear = vi.fn();
    useShortcutStore.getState().updateMouseShortcut("right", "terminal.clear");
    render(<Harness term={term} onPaste={onPaste} onClear={onClear} />);
    const surface = screen.getByTestId("surface");

    // tab.prev switches tab — must NOT refocus the old terminal
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 3, clientX: 20, clientY: 20, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 3, clientX: 20, clientY: 20, bubbles: true }));
    });
    expect(term.focus).not.toHaveBeenCalled();

    // terminal.paste refocuses terminal
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 1, clientX: 20, clientY: 20, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 20, clientY: 20, bubbles: true }));
    });
    expect(onPaste).toHaveBeenCalledTimes(1);
    expect(term.focus).toHaveBeenCalledTimes(1);

    // terminal.clear refocuses terminal
    surface.dispatchEvent(new MouseEvent("contextmenu", { button: 2, bubbles: true, cancelable: true }));
    expect(onClear).toHaveBeenCalledTimes(1);
    expect(term.focus).toHaveBeenCalledTimes(2);
  });

  it("calls onFocusPane on middle/side mousedown even when propagation is stopped", () => {
    const term = makeFakeTerm("");
    const onPaste = vi.fn();
    const onFocusPane = vi.fn();
    const parentMouseDown = vi.fn();
    render(
      <div onMouseDown={parentMouseDown}>
        <Harness term={term} onPaste={onPaste} onFocusPane={onFocusPane} />
      </div>
    );
    const surface = screen.getByTestId("surface");

    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 1, clientX: 25, clientY: 25, bubbles: true, cancelable: true })
    );
    expect(parentMouseDown).not.toHaveBeenCalled();
    expect(onFocusPane).toHaveBeenCalledTimes(1);

    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 25, clientY: 25, bubbles: true }));
    });
    expect(onPaste).toHaveBeenCalledTimes(1);
  });

  it("clears right-click pressRef after off-terminal release so subsequent keyboard contextmenu still opens", async () => {
    const term = makeFakeTerm("");
    render(<Harness term={term} />);
    const surface = screen.getByTestId("surface");

    // Press right button inside terminal, drag out and release outside (no contextmenu fires on terminal).
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 2, clientX: 10, clientY: 10, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 2, clientX: 400, clientY: 400, bubbles: true }));
    });

    // Flush the macrotask that clears the stale right-click pressRef.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });

    // Subsequent keyboard ContextMenu / Shift+F10 at a distant coordinate must NOT be suppressed as a drag.
    const kbCtxEv = new MouseEvent("contextmenu", {
      button: 2,
      clientX: 250,
      clientY: 250,
      bubbles: true,
      cancelable: true,
    });
    surface.dispatchEvent(kbCtxEv);
    expect(kbCtxEv.defaultPrevented).toBe(false);
  });

  it("dismisses an open floating selection popover immediately when selectionAction leaves popover mode", () => {
    const term = makeFakeTerm("selected text");
    render(<Harness term={term} />);
    const surface = screen.getByTestId("surface");

    fireEvent.mouseDown(surface, { button: 0, clientX: 20, clientY: 20 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 80, clientY: 80, bubbles: true }));
    });
    expect(screen.getByTestId("popover")).toBeInTheDocument();

    act(() => {
      useTerminalThemeStore.setState({ selectionAction: "auto-copy" });
    });
    expect(screen.queryByTestId("popover")).not.toBeInTheDocument();

    // Restore popover and verify switching to "none" also dismisses it.
    act(() => {
      useTerminalThemeStore.setState({ selectionAction: "popover" });
    });
    fireEvent.mouseDown(surface, { button: 0, clientX: 20, clientY: 20 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 80, clientY: 80, bubbles: true }));
    });
    expect(screen.getByTestId("popover")).toBeInTheDocument();

    act(() => {
      useTerminalThemeStore.setState({ selectionAction: "none" });
    });
    expect(screen.queryByTestId("popover")).not.toBeInTheDocument();
  });
});
