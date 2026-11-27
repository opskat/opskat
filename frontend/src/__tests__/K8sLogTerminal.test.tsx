import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ClipboardSetText } from "../../wailsjs/runtime/runtime";
import { K8sLogTerminal } from "@/components/k8s/K8sLogTerminal";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { DEFAULT_SHORTCUTS, useShortcutStore } from "@/stores/shortcutStore";
import { cloneDefaultMouseShortcuts } from "@/stores/terminalMouseShortcuts";
import { useTabStore } from "@/stores/tabStore";

const hoisted = vi.hoisted(() => {
  let selection = "";
  const term = {
    options: {} as Record<string, unknown>,
    modes: { mouseTrackingMode: "none" as const },
    loadAddon: vi.fn(),
    open: vi.fn(),
    write: vi.fn(),
    clear: vi.fn(),
    paste: vi.fn(),
    focus: vi.fn(),
    selectAll: vi.fn(),
    getSelection: vi.fn(() => selection),
    clearSelection: vi.fn(() => {
      selection = "";
    }),
    onSelectionChange: vi.fn(() => ({ dispose: vi.fn() })),
    dispose: vi.fn(),
  };
  return {
    term,
    setSelection(next: string) {
      selection = next;
    },
  };
});

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    constructor() {
      return hoisted.term;
    }
  },
}));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit = vi.fn();
  },
}));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

describe("K8sLogTerminal read-only mouse interaction", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      cb(0);
      return 1;
    });
    hoisted.setSelection("");
    useTerminalThemeStore.setState({
      selectionAction: "auto-copy",
    });
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      mouseShortcuts: cloneDefaultMouseShortcuts(),
      isRecording: false,
    });
    useTabStore.setState({
      tabs: [
        { id: "tab-1", type: "page", label: "one", meta: { type: "page", pageId: "settings" } },
        { id: "tab-2", type: "page", label: "two", meta: { type: "page", pageId: "sshkeys" } },
      ],
      activeTabId: "tab-2",
    });
  });

  it("auto-copies selected log text and clears selection immediately while skipping write actions", () => {
    render(<K8sLogTerminal />);
    const surface = screen.getByTestId("k8s-log-terminal");

    hoisted.setSelection("2026-11-25 INFO pod ready");
    fireEvent.mouseDown(surface, { button: 0, clientX: 10, clientY: 10 });
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 0, clientX: 120, clientY: 10, bubbles: true }));
    });

    expect(ClipboardSetText).toHaveBeenCalledWith("2026-11-25 INFO pod ready");
    expect(hoisted.term.clearSelection).toHaveBeenCalledTimes(1);

    // Middle click paste is ignored in readOnly mode
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 1, clientX: 10, clientY: 10, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 1, clientX: 10, clientY: 10, bubbles: true }));
    });
    expect(hoisted.term.paste).not.toHaveBeenCalled();

    // Side Back button still switches tabs
    surface.dispatchEvent(
      new MouseEvent("mousedown", { button: 3, clientX: 10, clientY: 10, bubbles: true, cancelable: true })
    );
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup", { button: 3, clientX: 10, clientY: 10, bubbles: true }));
    });
    expect(useTabStore.getState().activeTabId).toBe("tab-1");
  });
});
