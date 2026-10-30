import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac, type ShortcutBinding } from "../stores/shortcutStore";

// 表设计器的保存快捷键由设计器自己监听;全局处理不能吞掉它,否则别的视图(如远程文件编辑器的 Monaco
// ⌘S)再也收不到这个按键。
function press(target: HTMLElement, binding: ShortcutBinding): KeyboardEvent {
  const event = new KeyboardEvent("keydown", {
    code: binding.code,
    metaKey: isMac ? binding.mod : false,
    ctrlKey: isMac ? binding.ctrl : binding.mod,
    shiftKey: binding.shift,
    altKey: binding.alt,
    bubbles: true,
    cancelable: true,
  });
  target.dispatchEvent(event);
  return event;
}

describe("useKeyboardShortcuts — designer.save", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    useShortcutStore.setState({ shortcuts: { ...DEFAULT_SHORTCUTS }, isRecording: false });
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
  });

  it("全局处理不拦下保存快捷键:不阻止默认行为,事件照常传到编辑器", () => {
    // Monaco 的焦点元素是 EditContext 的 div,不是输入框。
    const editor = document.createElement("div");
    document.body.appendChild(editor);
    const received = vi.fn();
    editor.addEventListener("keydown", received);

    const event = press(editor, DEFAULT_SHORTCUTS["designer.save"]);
    expect(event.defaultPrevented).toBe(false);
    expect(received).toHaveBeenCalledTimes(1);
  });

  it("改过绑定后同样不拦下", () => {
    const binding: ShortcutBinding = { code: "KeyS", mod: true, ctrl: false, shift: true, alt: false };
    useShortcutStore.getState().updateShortcut("designer.save", binding);
    const editor = document.createElement("div");
    document.body.appendChild(editor);
    const received = vi.fn();
    editor.addEventListener("keydown", received);

    expect(press(editor, binding).defaultPrevented).toBe(false);
    expect(received).toHaveBeenCalledTimes(1);
  });
});
