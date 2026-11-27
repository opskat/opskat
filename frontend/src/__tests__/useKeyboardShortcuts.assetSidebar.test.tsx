import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import {
  useShortcutStore,
  DEFAULT_SHORTCUTS,
  findShortcutConflict,
  isMac,
  type ShortcutBinding,
} from "../stores/shortcutStore";

// 资产侧栏的全选 / 删除只在侧栏聚焦时由侧栏自己处理；全局处理不能吞掉这些按键，
// 否则编辑器、输入框里的 Delete / 全选都会失效。
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

describe("useKeyboardShortcuts — asset sidebar shortcuts", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    useShortcutStore.setState({ shortcuts: { ...DEFAULT_SHORTCUTS }, isRecording: false });
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
  });

  it.each(["asset.delete", "asset.selectAll"] as const)("全局处理不拦下 %s：事件照常传到聚焦元素", (action) => {
    const editor = document.createElement("div");
    document.body.appendChild(editor);
    const received = vi.fn();
    editor.addEventListener("keydown", received);

    const event = press(editor, DEFAULT_SHORTCUTS[action]);
    expect(event.defaultPrevented).toBe(false);
    expect(received).toHaveBeenCalledTimes(1);
  });

  it("侧栏全选与终端全选默认同键，各管各的焦点区域，不算冲突", () => {
    const { shortcuts } = useShortcutStore.getState();
    expect(findShortcutConflict("asset.selectAll", DEFAULT_SHORTCUTS["asset.selectAll"], shortcuts)).toBeNull();
    expect(findShortcutConflict("terminal.selectAll", DEFAULT_SHORTCUTS["terminal.selectAll"], shortcuts)).toBeNull();
  });
});
