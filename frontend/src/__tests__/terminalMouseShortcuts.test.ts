import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  applyShortcutsBackup,
  DEFAULT_SHORTCUTS,
  serializeShortcutsForBackup,
  useShortcutStore,
} from "@/stores/shortcutStore";
import {
  cloneDefaultMouseShortcuts,
  DEFAULT_TERMINAL_MOUSE_BINDINGS,
  migrateLegacyCopyBehaviorStorage,
  MOUSE_SHORTCUTS_BACKUP_KEY,
  normalizeMouseShortcuts,
  TERMINAL_MOUSE_ACTIONS,
  TERMINAL_MOUSE_BUTTONS,
  TERMINAL_MOUSE_STORAGE_KEY,
  TERMINAL_THEME_STORAGE_KEY,
} from "@/stores/terminalMouseShortcuts";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";

describe("terminalMouseShortcuts & shortcutStore integration", () => {
  beforeEach(() => {
    localStorage.clear();
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      mouseShortcuts: cloneDefaultMouseShortcuts(),
      isRecording: false,
      disableShortcutsInTerminal: false,
    });
  });

  describe("fixed 4-button defaults, updates & normalization", () => {
    it("ships with the 4 locked default button slots and trimmed 9-action set", () => {
      expect(TERMINAL_MOUSE_BUTTONS).toEqual(["right", "middle", "back", "forward"]);
      expect(TERMINAL_MOUSE_ACTIONS).toEqual([
        "none",
        "terminal.contextMenu",
        "terminal.copyOrPaste",
        "terminal.copy",
        "terminal.paste",
        "terminal.clear",
        "tab.prev",
        "tab.next",
        "tab.close",
      ]);
      expect(useShortcutStore.getState().mouseShortcuts).toEqual({
        right: "terminal.contextMenu",
        middle: "terminal.paste",
        back: "tab.prev",
        forward: "tab.next",
      });
    });

    it("persists only customized slots to localStorage and clears storage when reset to defaults", () => {
      useShortcutStore.getState().updateMouseShortcut("right", "terminal.copyOrPaste");
      expect(useShortcutStore.getState().mouseShortcuts.right).toBe("terminal.copyOrPaste");
      expect(JSON.parse(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)!)).toEqual({
        right: "terminal.copyOrPaste",
      });

      useShortcutStore.getState().updateMouseShortcut("middle", "terminal.clear");
      expect(JSON.parse(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)!)).toEqual({
        right: "terminal.copyOrPaste",
        middle: "terminal.clear",
      });

      useShortcutStore.getState().resetMouseShortcut("right");
      expect(useShortcutStore.getState().mouseShortcuts.right).toBe("terminal.contextMenu");
      expect(JSON.parse(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)!)).toEqual({
        middle: "terminal.clear",
      });

      useShortcutStore.getState().resetAllMouseShortcuts();
      expect(useShortcutStore.getState().mouseShortcuts).toEqual(DEFAULT_TERMINAL_MOUSE_BINDINGS);
      expect(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)).toBeNull();
    });

    it("ignores invalid buttons, removed actions, and malformed payloads during normalization", () => {
      expect(normalizeMouseShortcuts(null)).toEqual(DEFAULT_TERMINAL_MOUSE_BINDINGS);
      expect(normalizeMouseShortcuts([])).toEqual(DEFAULT_TERMINAL_MOUSE_BINDINGS);
      expect(
        normalizeMouseShortcuts({
          right: "terminal.paste",
          middle: "terminal.find", // removed action -> falls back to default
          back: "none",
          left: "terminal.clear", // unsupported button -> ignored
        })
      ).toEqual({
        right: "terminal.paste",
        middle: "terminal.paste",
        back: "none",
        forward: "tab.next",
      });
    });
  });

  describe("one-time legacy copyBehavior migration", () => {
    it("migrates popover-menu -> selectionAction:popover and default right click", () => {
      localStorage.setItem(
        TERMINAL_THEME_STORAGE_KEY,
        JSON.stringify({ state: { copyBehavior: "popover-menu", fontSize: 16 }, version: 0 })
      );

      migrateLegacyCopyBehaviorStorage();

      const theme = JSON.parse(localStorage.getItem(TERMINAL_THEME_STORAGE_KEY)!);
      expect(theme.state.copyBehavior).toBeUndefined();
      expect(theme.state.selectionAction).toBe("popover");
      expect(theme.state.fontSize).toBe(16);
      expect(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)).toBeNull();
    });

    it("migrates smart-right-click -> selectionAction:none and right:terminal.copyOrPaste", () => {
      localStorage.setItem(
        TERMINAL_THEME_STORAGE_KEY,
        JSON.stringify({ state: { copyBehavior: "smart-right-click" }, version: 0 })
      );

      migrateLegacyCopyBehaviorStorage();

      const theme = JSON.parse(localStorage.getItem(TERMINAL_THEME_STORAGE_KEY)!);
      expect(theme.state.copyBehavior).toBeUndefined();
      expect(theme.state.selectionAction).toBe("none");
      expect(JSON.parse(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)!)).toEqual({
        right: "terminal.copyOrPaste",
      });
    });

    it("migrates select-copy-right-paste -> selectionAction:auto-copy and right:terminal.paste", () => {
      localStorage.setItem(
        TERMINAL_THEME_STORAGE_KEY,
        JSON.stringify({ state: { copyBehavior: "select-copy-right-paste" }, version: 0 })
      );

      migrateLegacyCopyBehaviorStorage();

      const theme = JSON.parse(localStorage.getItem(TERMINAL_THEME_STORAGE_KEY)!);
      expect(theme.state.copyBehavior).toBeUndefined();
      expect(theme.state.selectionAction).toBe("auto-copy");
      expect(JSON.parse(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)!)).toEqual({
        right: "terminal.paste",
      });
    });

    it("does not overwrite existing terminal_mouse_shortcuts during migration", () => {
      localStorage.setItem(TERMINAL_MOUSE_STORAGE_KEY, JSON.stringify({ right: "terminal.clear" }));
      localStorage.setItem(
        TERMINAL_THEME_STORAGE_KEY,
        JSON.stringify({ state: { copyBehavior: "select-copy-right-paste" }, version: 0 })
      );

      migrateLegacyCopyBehaviorStorage();

      expect(JSON.parse(localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY)!)).toEqual({
        right: "terminal.clear",
      });
    });

    it("hydrates migrated state cleanly into both terminalThemeStore and shortcutStore", async () => {
      useTerminalThemeStore.setState({ selectionAction: "popover" });
      localStorage.setItem(
        TERMINAL_THEME_STORAGE_KEY,
        JSON.stringify({ state: { copyBehavior: "select-copy-right-paste" }, version: 0 })
      );

      migrateLegacyCopyBehaviorStorage();
      await useTerminalThemeStore.persist.rehydrate();
      vi.resetModules();
      const { useShortcutStore: freshShortcutStore } = await import("@/stores/shortcutStore");

      expect(useTerminalThemeStore.getState().selectionAction).toBe("auto-copy");
      expect(freshShortcutStore.getState().mouseShortcuts.right).toBe("terminal.paste");
      expect(freshShortcutStore.getState().mouseShortcuts.middle).toBe("terminal.paste");
    });
  });

  describe("backup serialization & backward compatibility", () => {
    it("returns null when both keyboard and mouse shortcuts are at defaults", () => {
      const { shortcuts, mouseShortcuts } = useShortcutStore.getState();
      expect(serializeShortcutsForBackup(shortcuts, mouseShortcuts)).toBeNull();
    });

    it("keeps legacy flat JSON shape when only keyboard shortcuts are customized", () => {
      useShortcutStore
        .getState()
        .updateShortcut("tab.close", { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false });
      const { shortcuts, mouseShortcuts } = useShortcutStore.getState();
      const serialized = serializeShortcutsForBackup(shortcuts, mouseShortcuts)!;
      const parsed = JSON.parse(serialized);
      expect(parsed["tab.close"].code).toBe("KeyQ");
      expect(parsed[MOUSE_SHORTCUTS_BACKUP_KEY]).toBeUndefined();
    });

    it("stores only the compact customized slot diff and round-trips with keyboard shortcuts", () => {
      useShortcutStore
        .getState()
        .updateShortcut("tab.close", { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false });
      useShortcutStore.getState().updateMouseShortcut("right", "terminal.copyOrPaste");
      useShortcutStore.getState().updateMouseShortcut("back", "none");

      const { shortcuts, mouseShortcuts } = useShortcutStore.getState();
      const backupJson = serializeShortcutsForBackup(shortcuts, mouseShortcuts)!;
      const parsed = JSON.parse(backupJson);
      expect(parsed[MOUSE_SHORTCUTS_BACKUP_KEY]).toEqual({
        right: "terminal.copyOrPaste",
        back: "none",
      });

      useShortcutStore.getState().resetAll();
      useShortcutStore.getState().resetAllMouseShortcuts();
      applyShortcutsBackup(backupJson);

      expect(useShortcutStore.getState().shortcuts["tab.close"].code).toBe("KeyQ");
      expect(useShortcutStore.getState().mouseShortcuts).toEqual({
        right: "terminal.copyOrPaste",
        middle: "terminal.paste",
        back: "none",
        forward: "tab.next",
      });
    });

    it("imports older backups (no $mouseShortcuts key) without wiping existing mouse shortcuts", () => {
      useShortcutStore.getState().updateMouseShortcut("middle", "terminal.clear");

      const legacyBackup = JSON.stringify({
        "tab.close": { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false },
      });
      applyShortcutsBackup(legacyBackup);

      expect(useShortcutStore.getState().shortcuts["tab.close"].code).toBe("KeyQ");
      expect(useShortcutStore.getState().mouseShortcuts.middle).toBe("terminal.clear");
    });
  });
});
