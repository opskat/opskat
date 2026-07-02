import { describe, it, expect, beforeEach } from "vitest";
import {
  useShortcutStore,
  DEFAULT_SHORTCUTS,
  findShortcutConflict,
  type ShortcutBinding,
} from "../stores/shortcutStore";

describe("shortcutStore", () => {
  beforeEach(() => {
    localStorage.clear();
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      isRecording: false,
      disableShortcutsInTerminal: false,
    });
  });

  describe("initial state", () => {
    it("has all default shortcuts", () => {
      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["tab.1"]).toEqual(DEFAULT_SHORTCUTS["tab.1"]);
      expect(shortcuts["tab.close"]).toEqual(DEFAULT_SHORTCUTS["tab.close"]);
      expect(shortcuts["panel.ai"]).toEqual(DEFAULT_SHORTCUTS["panel.ai"]);
    });

    it("has panel.switch default", () => {
      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["panel.switch"]).toEqual(DEFAULT_SHORTCUTS["panel.switch"]);
    });

    it("has panel.filter default", () => {
      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["panel.filter"]).toEqual(DEFAULT_SHORTCUTS["panel.filter"]);
    });

    it("has terminal shortcut defaults", () => {
      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["terminal.copy"]).toEqual(DEFAULT_SHORTCUTS["terminal.copy"]);
      expect(shortcuts["terminal.paste"]).toEqual(DEFAULT_SHORTCUTS["terminal.paste"]);
      expect(shortcuts["terminal.selectAll"]).toEqual(DEFAULT_SHORTCUTS["terminal.selectAll"]);
      expect(shortcuts["terminal.find"]).toEqual(DEFAULT_SHORTCUTS["terminal.find"]);
    });

    it("isRecording is false", () => {
      expect(useShortcutStore.getState().isRecording).toBe(false);
    });
  });

  describe("updateShortcut", () => {
    it("updates a single shortcut binding", () => {
      const newBinding: ShortcutBinding = { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false };
      useShortcutStore.getState().updateShortcut("tab.close", newBinding);

      expect(useShortcutStore.getState().shortcuts["tab.close"]).toEqual(newBinding);
    });

    it("updating terminal.copy does not change any other shortcut", () => {
      const newBinding: ShortcutBinding = { code: "KeyY", mod: true, ctrl: false, shift: false, alt: false };
      useShortcutStore.getState().updateShortcut("terminal.copy", newBinding);

      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["terminal.copy"]).toEqual(newBinding);
      for (const [action, binding] of Object.entries(DEFAULT_SHORTCUTS)) {
        if (action === "terminal.copy") continue;
        expect(shortcuts[action as keyof typeof DEFAULT_SHORTCUTS]).toEqual(binding);
      }
    });

    it("persists custom shortcuts to localStorage", () => {
      const newBinding: ShortcutBinding = { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false };
      useShortcutStore.getState().updateShortcut("tab.close", newBinding);

      const stored = JSON.parse(localStorage.getItem("keyboard_shortcuts")!);
      expect(stored["tab.close"]).toEqual(newBinding);
    });

    it("does not persist shortcuts that match defaults", () => {
      // Set to non-default, then back to default
      const custom: ShortcutBinding = { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false };
      useShortcutStore.getState().updateShortcut("tab.close", custom);
      useShortcutStore.getState().updateShortcut("tab.close", DEFAULT_SHORTCUTS["tab.close"]);

      expect(localStorage.getItem("keyboard_shortcuts")).toBeNull();
    });
  });

  describe("resetShortcut", () => {
    it("resets a single shortcut to default", () => {
      const custom: ShortcutBinding = { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false };
      useShortcutStore.getState().updateShortcut("tab.close", custom);
      useShortcutStore.getState().resetShortcut("tab.close");

      expect(useShortcutStore.getState().shortcuts["tab.close"]).toEqual(DEFAULT_SHORTCUTS["tab.close"]);
    });
  });

  describe("resetAll", () => {
    it("resets all shortcuts to defaults and clears localStorage", () => {
      const custom: ShortcutBinding = { code: "KeyQ", mod: true, ctrl: false, shift: false, alt: false };
      useShortcutStore.getState().updateShortcut("tab.close", custom);
      useShortcutStore
        .getState()
        .updateShortcut("tab.1", { code: "KeyA", mod: true, ctrl: false, shift: false, alt: false });

      useShortcutStore.getState().resetAll();

      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["tab.close"]).toEqual(DEFAULT_SHORTCUTS["tab.close"]);
      expect(shortcuts["tab.1"]).toEqual(DEFAULT_SHORTCUTS["tab.1"]);
      expect(localStorage.getItem("keyboard_shortcuts")).toBeNull();
    });
  });

  describe("swapCmdCtrl", () => {
    it("swaps mod and ctrl for every binding (Cmd defaults become Ctrl)", () => {
      useShortcutStore.getState().swapCmdCtrl();

      const { shortcuts } = useShortcutStore.getState();
      expect(shortcuts["tab.1"]).toEqual({ ...DEFAULT_SHORTCUTS["tab.1"], mod: false, ctrl: true });
      // shift/alt are untouched by the swap
      expect(shortcuts["tab.prev"]).toEqual({ ...DEFAULT_SHORTCUTS["tab.prev"], mod: false, ctrl: true });
    });

    it("is reversible: swapping twice restores defaults and clears localStorage", () => {
      useShortcutStore.getState().swapCmdCtrl();
      useShortcutStore.getState().swapCmdCtrl();

      expect(useShortcutStore.getState().shortcuts["tab.1"]).toEqual(DEFAULT_SHORTCUTS["tab.1"]);
      expect(localStorage.getItem("keyboard_shortcuts")).toBeNull();
    });

    it("persists the swapped bindings to localStorage", () => {
      useShortcutStore.getState().swapCmdCtrl();

      const stored = JSON.parse(localStorage.getItem("keyboard_shortcuts")!);
      expect(stored["tab.1"]).toEqual({ ...DEFAULT_SHORTCUTS["tab.1"], mod: false, ctrl: true });
    });
  });

  describe("setIsRecording", () => {
    it("toggles recording state", () => {
      useShortcutStore.getState().setIsRecording(true);
      expect(useShortcutStore.getState().isRecording).toBe(true);

      useShortcutStore.getState().setIsRecording(false);
      expect(useShortcutStore.getState().isRecording).toBe(false);
    });
  });

  describe("disableShortcutsInTerminal", () => {
    it("defaults to false", () => {
      expect(useShortcutStore.getState().disableShortcutsInTerminal).toBe(false);
    });

    it("setDisableShortcutsInTerminal updates state and persists when enabled", () => {
      useShortcutStore.getState().setDisableShortcutsInTerminal(true);

      expect(useShortcutStore.getState().disableShortcutsInTerminal).toBe(true);
      expect(localStorage.getItem("disable_shortcuts_in_terminal")).toBe("true");
    });

    it("persists false when turned back off", () => {
      useShortcutStore.getState().setDisableShortcutsInTerminal(true);
      useShortcutStore.getState().setDisableShortcutsInTerminal(false);

      expect(useShortcutStore.getState().disableShortcutsInTerminal).toBe(false);
      expect(localStorage.getItem("disable_shortcuts_in_terminal")).toBe("false");
    });
  });

  describe("findShortcutConflict", () => {
    it("reports conflicts with existing bindings", () => {
      expect(findShortcutConflict("terminal.copy", DEFAULT_SHORTCUTS["tab.close"], DEFAULT_SHORTCUTS)).toBe(
        "tab.close"
      );
    });

    it("allows terminal.find and panel.filter to share the same default binding", () => {
      expect(findShortcutConflict("terminal.find", DEFAULT_SHORTCUTS["panel.filter"], DEFAULT_SHORTCUTS)).toBeNull();
    });
  });
});
