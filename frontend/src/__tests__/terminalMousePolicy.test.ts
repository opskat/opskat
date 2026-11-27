import { describe, expect, it } from "vitest";
import {
  isMacCtrlLeftContextMenu,
  isMouseDrag,
  MOUSE_DRAG_THRESHOLD_PX,
  resolveTerminalMousePolicy,
  shouldAutoCopySelection,
  shouldPreventMouseDownDefault,
  type MouseEventSnapshot,
  type MousePolicyContext,
} from "@/components/terminal/terminalMousePolicy";
import { cloneDefaultMouseShortcuts } from "@/stores/terminalMouseShortcuts";

function ev(overrides: Partial<MouseEventSnapshot> = {}): MouseEventSnapshot {
  return {
    button: 0,
    metaKey: false,
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    ...overrides,
  };
}

function ctx(overrides: Partial<MousePolicyContext> = {}): MousePolicyContext {
  return {
    bindings: cloneDefaultMouseShortcuts(),
    mouseTrackingMode: "none",
    hasSelection: false,
    readOnly: false,
    wasDragged: false,
    ...overrides,
  };
}

describe("terminalMousePolicy", () => {
  describe("isMouseDrag", () => {
    it("treats movement within threshold as a click and beyond threshold as a drag", () => {
      expect(isMouseDrag(100, 100, 100 + MOUSE_DRAG_THRESHOLD_PX, 100)).toBe(false);
      expect(isMouseDrag(100, 100, 103, 104)).toBe(false); // hypot(3,4)=5
      expect(isMouseDrag(100, 100, 104, 104)).toBe(true); // hypot(4,4)>5
      expect(isMouseDrag(100, 100, 110, 100)).toBe(true);
    });
  });

  describe("default bindings in normal terminal mode", () => {
    it("maps bare right click to contextMenu, middle to paste, back to tab.prev, forward to tab.next", () => {
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), ctx())).toEqual({ kind: "contextMenu" });
      expect(resolveTerminalMousePolicy(ev({ button: 1 }), ctx())).toEqual({
        kind: "action",
        action: "terminal.paste",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 3 }), ctx())).toEqual({
        kind: "action",
        action: "tab.prev",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 4 }), ctx())).toEqual({
        kind: "action",
        action: "tab.next",
      });
    });

    it("always passes left click through regardless of modifiers", () => {
      expect(resolveTerminalMousePolicy(ev({ button: 0 }), ctx())).toEqual({ kind: "passthrough" });
      expect(resolveTerminalMousePolicy(ev({ button: 0, ctrlKey: true }), ctx())).toEqual({ kind: "passthrough" });
      expect(resolveTerminalMousePolicy(ev({ button: 0, metaKey: true }), ctx())).toEqual({ kind: "passthrough" });
      expect(resolveTerminalMousePolicy(ev({ button: 0, shiftKey: true }), ctx())).toEqual({ kind: "passthrough" });
    });

    it("identifies synthetic macOS Ctrl+Left contextmenu events", () => {
      expect(isMacCtrlLeftContextMenu({ ctrlKey: true }, 0, true)).toBe(true);
      expect(isMacCtrlLeftContextMenu({ ctrlKey: true }, 2, true)).toBe(false);
      expect(isMacCtrlLeftContextMenu({ ctrlKey: true }, 0, false)).toBe(false);
    });
  });

  describe("drag guard", () => {
    it("suppresses right click menu/action when dragged > 5px", () => {
      const bindings = { ...cloneDefaultMouseShortcuts(), right: "terminal.paste" as const };
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings, wasDragged: true }))).toEqual({
        kind: "suppress",
      });
    });

    it("passes through middle and side clicks without firing action when dragged", () => {
      expect(resolveTerminalMousePolicy(ev({ button: 1 }), ctx({ wasDragged: true }))).toEqual({
        kind: "passthrough",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 3 }), ctx({ wasDragged: true }))).toEqual({
        kind: "passthrough",
      });
    });
  });

  describe("smart copyOrPaste and right-click escape hatch", () => {
    it("resolves copyOrPaste to copy when selection exists, and paste when empty", () => {
      const bindings = { ...cloneDefaultMouseShortcuts(), right: "terminal.copyOrPaste" as const };
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings, hasSelection: true }))).toEqual({
        kind: "action",
        action: "terminal.copy",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings, hasSelection: false }))).toEqual({
        kind: "action",
        action: "terminal.paste",
      });
    });

    it("keeps Shift+Right as contextMenu escape hatch when bare right click is rebound", () => {
      const bindings = { ...cloneDefaultMouseShortcuts(), right: "terminal.paste" as const };
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings }))).toEqual({
        kind: "action",
        action: "terminal.paste",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 2, shiftKey: true }), ctx({ bindings }))).toEqual({
        kind: "contextMenu",
      });
    });

    it("suppresses contextMenu when bare right click is set to none while keeping Shift+Right escape hatch", () => {
      const bindings = { ...cloneDefaultMouseShortcuts(), right: "none" as const };
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings }))).toEqual({ kind: "suppress" });
      expect(resolveTerminalMousePolicy(ev({ button: 2, shiftKey: true }), ctx({ bindings }))).toEqual({
        kind: "contextMenu",
      });
    });
  });

  describe("remote TUI mouse tracking (vim/tmux/htop)", () => {
    it("passes left/middle through and suppresses OpsKat right-click menu when tracking is active without Shift", () => {
      const tuiCtx = ctx({ mouseTrackingMode: "vt200" });
      expect(resolveTerminalMousePolicy(ev({ button: 0 }), tuiCtx)).toEqual({ kind: "passthrough" });
      expect(resolveTerminalMousePolicy(ev({ button: 1 }), tuiCtx)).toEqual({ kind: "passthrough" });
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), tuiCtx)).toEqual({ kind: "suppress" });
    });

    it("restores OpsKat middle/right actions when Shift bypass is held in TUI", () => {
      const tuiCtx = ctx({ mouseTrackingMode: "any" });
      expect(resolveTerminalMousePolicy(ev({ button: 1, shiftKey: true }), tuiCtx)).toEqual({
        kind: "action",
        action: "terminal.paste",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 2, shiftKey: true }), tuiCtx)).toEqual({
        kind: "contextMenu",
      });
    });

    it("keeps Shift+Left passing through in TUI so force-selection works", () => {
      const tuiCtx = ctx({ mouseTrackingMode: "drag" });
      expect(resolveTerminalMousePolicy(ev({ button: 0, shiftKey: true }), tuiCtx)).toEqual({
        kind: "passthrough",
      });
    });

    it("keeps side buttons (Back/Forward) active regardless of TUI mouse tracking", () => {
      const tuiCtx = ctx({ mouseTrackingMode: "vt200" });
      expect(resolveTerminalMousePolicy(ev({ button: 3 }), tuiCtx)).toEqual({
        kind: "action",
        action: "tab.prev",
      });
      expect(resolveTerminalMousePolicy(ev({ button: 4 }), tuiCtx)).toEqual({
        kind: "action",
        action: "tab.next",
      });
    });
  });

  describe("read-only mode (K8sLogTerminal)", () => {
    it("skips paste and clear while allowing copy, contextMenu, and workspace nav", () => {
      const roCtx = ctx({ readOnly: true });
      expect(resolveTerminalMousePolicy(ev({ button: 1 }), roCtx)).toEqual({ kind: "passthrough" });
      expect(resolveTerminalMousePolicy(ev({ button: 2 }), roCtx)).toEqual({ kind: "contextMenu" });
      expect(resolveTerminalMousePolicy(ev({ button: 3 }), roCtx)).toEqual({
        kind: "action",
        action: "tab.prev",
      });

      const bindings = {
        ...cloneDefaultMouseShortcuts(),
        right: "terminal.copyOrPaste" as const,
        middle: "terminal.clear" as const,
      };

      expect(
        resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings, readOnly: true, hasSelection: true }))
      ).toEqual({ kind: "action", action: "terminal.copy" });
      expect(
        resolveTerminalMousePolicy(ev({ button: 2 }), ctx({ bindings, readOnly: true, hasSelection: false }))
      ).toEqual({ kind: "suppress" });
      expect(resolveTerminalMousePolicy(ev({ button: 1 }), ctx({ bindings, readOnly: true }))).toEqual({
        kind: "passthrough",
      });
    });
  });

  describe("shouldPreventMouseDownDefault", () => {
    it("prevents default on middle click when bound, not when TUI tracks mouse without Shift", () => {
      expect(shouldPreventMouseDownDefault(ev({ button: 1 }), ctx())).toBe(true);
      expect(shouldPreventMouseDownDefault(ev({ button: 1 }), ctx({ mouseTrackingMode: "vt200" }))).toBe(false);
      expect(
        shouldPreventMouseDownDefault(ev({ button: 1, shiftKey: true }), ctx({ mouseTrackingMode: "vt200" }))
      ).toBe(true);
    });

    it("always prevents default on side buttons (3/4) to block WebView history navigation", () => {
      const disabledBindings = {
        right: "none" as const,
        middle: "none" as const,
        back: "none" as const,
        forward: "none" as const,
      };
      expect(shouldPreventMouseDownDefault(ev({ button: 3 }), ctx({ bindings: disabledBindings }))).toBe(true);
      expect(shouldPreventMouseDownDefault(ev({ button: 4 }), ctx({ bindings: disabledBindings }))).toBe(true);
    });

    it("never prevents mousedown default on left or right click", () => {
      expect(shouldPreventMouseDownDefault(ev({ button: 0 }), ctx())).toBe(false);
      expect(shouldPreventMouseDownDefault(ev({ button: 2 }), ctx())).toBe(false);
    });
  });

  describe("shouldAutoCopySelection", () => {
    it("copies non-whitespace text only in auto-copy mode and skips empty, whitespace-only, or duplicate", () => {
      expect(shouldAutoCopySelection("ls -la", "auto-copy", "")).toBe("ls -la");
      expect(shouldAutoCopySelection("ls -la", "popover", "")).toBeNull();
      expect(shouldAutoCopySelection("ls -la", "none", "")).toBeNull();
      expect(shouldAutoCopySelection("", "auto-copy", "")).toBeNull();
      expect(shouldAutoCopySelection("   \t\r\n  ", "auto-copy", "")).toBeNull();
      expect(shouldAutoCopySelection("ls -la", "auto-copy", "ls -la")).toBeNull();
    });
  });
});
