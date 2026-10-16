import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac } from "../stores/shortcutStore";
import { useLayoutStore } from "../stores/layoutStore";

function press(action: "panel.filter" | "tab.close", target: HTMLElement): KeyboardEvent {
  const binding = DEFAULT_SHORTCUTS[action];
  const ev = new KeyboardEvent("keydown", {
    code: binding.code,
    metaKey: isMac ? binding.mod : false,
    ctrlKey: isMac ? binding.ctrl : binding.mod,
    shiftKey: binding.shift,
    altKey: binding.alt,
    bubbles: true,
    cancelable: true,
  });
  target.focus();
  target.dispatchEvent(ev);
  return ev;
}

// 真实结构：Monaco 的焦点元素是 EditContext 的 div.native-edit-context，
// 既不是 input/textarea，也不是 contenteditable。
function monacoFocusTarget(): HTMLElement {
  const editor = document.createElement("div");
  editor.className = "monaco-editor";
  const ctx = document.createElement("div");
  ctx.className = "native-edit-context";
  ctx.tabIndex = 0;
  editor.appendChild(ctx);
  document.body.appendChild(editor);
  return ctx;
}

describe("useKeyboardShortcuts — Monaco editor focus", () => {
  let requestOpenFilter: ReturnType<typeof vi.fn<() => void>>;

  beforeEach(() => {
    document.body.innerHTML = "";
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      isRecording: false,
      disableShortcutsInTerminal: false,
    });
    requestOpenFilter = vi.fn<() => void>();
    useLayoutStore.setState({ requestOpenFilter });
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
  });

  it("leaves Cmd/Ctrl+F to Monaco so its find widget opens instead of the tab filter", () => {
    const ev = press("panel.filter", monacoFocusTarget());

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("still opens the tab filter when focus is outside any editor", () => {
    const el = document.createElement("div");
    el.tabIndex = 0;
    document.body.appendChild(el);

    const ev = press("panel.filter", el);

    expect(requestOpenFilter).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("keeps handling other global shortcuts inside Monaco (Cmd/Ctrl+W)", () => {
    const ev = press("tab.close", monacoFocusTarget());

    expect(ev.defaultPrevented).toBe(true);
  });
});
