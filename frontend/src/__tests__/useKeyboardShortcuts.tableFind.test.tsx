import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac } from "../stores/shortcutStore";
import { useLayoutStore } from "../stores/layoutStore";
import { TABLE_FIND_SCOPE_ATTR } from "../lib/tableFind";

function pressFind(target: HTMLElement): KeyboardEvent {
  const binding = DEFAULT_SHORTCUTS["panel.filter"];
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

function focusable(parent: HTMLElement, tag = "div"): HTMLElement {
  const el = document.createElement(tag);
  if (tag === "div") el.tabIndex = 0;
  parent.appendChild(el);
  return el;
}

describe("useKeyboardShortcuts — Cmd/Ctrl+F dispatch by focus", () => {
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

  it("leaves the key to a database result grid so it can open its find bar", () => {
    const region = document.createElement("div");
    region.setAttribute(TABLE_FIND_SCOPE_ATTR, "");
    document.body.appendChild(region);
    const grid = focusable(region);

    const ev = pressFind(grid);

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });

  it("still opens the tab filter when focus is anywhere else", () => {
    const ev = pressFind(focusable(document.body));

    expect(requestOpenFilter).toHaveBeenCalledTimes(1);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("does not take the key from the SQL editor (Monaco keeps its own find)", () => {
    const ev = pressFind(focusable(document.body, "textarea"));

    expect(requestOpenFilter).not.toHaveBeenCalled();
    expect(ev.defaultPrevented).toBe(false);
  });
});
