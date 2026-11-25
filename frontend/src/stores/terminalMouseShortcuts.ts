export type TerminalSelectionAction = "popover" | "auto-copy" | "none";

export const TERMINAL_SELECTION_ACTIONS: readonly TerminalSelectionAction[] = ["popover", "auto-copy", "none"] as const;

const VALID_SELECTION_ACTIONS = new Set<string>(TERMINAL_SELECTION_ACTIONS);

export function isTerminalSelectionAction(value: unknown): value is TerminalSelectionAction {
  return typeof value === "string" && VALID_SELECTION_ACTIONS.has(value);
}

export type TerminalMouseButton = "right" | "middle" | "back" | "forward";

export const TERMINAL_MOUSE_BUTTONS: readonly TerminalMouseButton[] = ["right", "middle", "back", "forward"] as const;

export function terminalMouseButtonFromCode(button: number): TerminalMouseButton | null {
  switch (button) {
    case 1:
      return "middle";
    case 2:
      return "right";
    case 3:
      return "back";
    case 4:
      return "forward";
    default:
      return null;
  }
}

export type TerminalMouseAction =
  | "none"
  | "terminal.contextMenu"
  | "terminal.copyOrPaste"
  | "terminal.copy"
  | "terminal.paste"
  | "terminal.clear"
  | "tab.prev"
  | "tab.next"
  | "tab.close";

export const TERMINAL_MOUSE_TERMINAL_ACTIONS: readonly TerminalMouseAction[] = [
  "terminal.contextMenu",
  "terminal.copyOrPaste",
  "terminal.copy",
  "terminal.paste",
  "terminal.clear",
] as const;

export const TERMINAL_MOUSE_WORKSPACE_ACTIONS: readonly TerminalMouseAction[] = [
  "tab.prev",
  "tab.next",
  "tab.close",
] as const;

export const TERMINAL_MOUSE_ACTIONS: readonly TerminalMouseAction[] = [
  "none",
  ...TERMINAL_MOUSE_TERMINAL_ACTIONS,
  ...TERMINAL_MOUSE_WORKSPACE_ACTIONS,
] as const;

const VALID_ACTIONS = new Set<string>(TERMINAL_MOUSE_ACTIONS);

export type TerminalMouseBindings = Record<TerminalMouseButton, TerminalMouseAction>;

export const TERMINAL_THEME_STORAGE_KEY = "terminal_theme";
export const TERMINAL_MOUSE_STORAGE_KEY = "terminal_mouse_shortcuts";
export const MOUSE_SHORTCUTS_BACKUP_KEY = "$mouseShortcuts";

export const DEFAULT_TERMINAL_MOUSE_BINDINGS: Readonly<TerminalMouseBindings> = {
  right: "terminal.contextMenu",
  middle: "terminal.paste",
  back: "tab.prev",
  forward: "tab.next",
};

export function cloneDefaultMouseShortcuts(): TerminalMouseBindings {
  return { ...DEFAULT_TERMINAL_MOUSE_BINDINGS };
}

export function mouseShortcutsEqual(a: TerminalMouseBindings, b: TerminalMouseBindings): boolean {
  return TERMINAL_MOUSE_BUTTONS.every((button) => a[button] === b[button]);
}

/** Return only the slots that differ from DEFAULT_TERMINAL_MOUSE_BINDINGS (for compact storage and backup). */
export function customMouseShortcuts(bindings: TerminalMouseBindings): Partial<TerminalMouseBindings> {
  const custom: Partial<TerminalMouseBindings> = {};
  for (const button of TERMINAL_MOUSE_BUTTONS) {
    if (bindings[button] !== DEFAULT_TERMINAL_MOUSE_BINDINGS[button]) {
      custom[button] = bindings[button];
    }
  }
  return custom;
}

/**
 * Normalize an untrusted mouse-shortcut map (from localStorage or backup import)
 * into a complete 4-slot TerminalMouseBindings object, ignoring invalid keys/actions.
 */
export function normalizeMouseShortcuts(raw: unknown): TerminalMouseBindings {
  const result = cloneDefaultMouseShortcuts();
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return result;

  const obj = raw as Record<string, unknown>;
  for (const button of TERMINAL_MOUSE_BUTTONS) {
    const candidate = obj[button];
    if (typeof candidate === "string" && VALID_ACTIONS.has(candidate)) {
      result[button] = candidate as TerminalMouseAction;
    }
  }
  return result;
}

interface LegacyCopyBehaviorMigration {
  selectionAction: TerminalSelectionAction;
  rightClickAction: TerminalMouseAction;
}

export function mapLegacyCopyBehavior(value: unknown): LegacyCopyBehaviorMigration | null {
  switch (value) {
    case "popover-menu":
      return { selectionAction: "popover", rightClickAction: "terminal.contextMenu" };
    case "smart-right-click":
      return { selectionAction: "none", rightClickAction: "terminal.copyOrPaste" };
    case "select-copy-right-paste":
      return { selectionAction: "auto-copy", rightClickAction: "terminal.paste" };
    default:
      return null;
  }
}

/**
 * One-time lossless migration from the retired `copyBehavior` field in `localStorage["terminal_theme"]`
 * into `selectionAction` and `localStorage["terminal_mouse_shortcuts"]`.
 * Strips `copyBehavior` immediately after migrating so it runs once and never conflicts at runtime.
 */
export function migrateLegacyCopyBehaviorStorage(): void {
  try {
    const rawTheme = localStorage.getItem(TERMINAL_THEME_STORAGE_KEY);
    if (!rawTheme) return;
    const parsed = JSON.parse(rawTheme) as { state?: Record<string, unknown>; version?: number };
    const state = parsed?.state;
    if (!state || typeof state !== "object" || !("copyBehavior" in state)) return;

    const mapped = mapLegacyCopyBehavior(state.copyBehavior);
    delete state.copyBehavior;

    if (mapped) {
      if (!isTerminalSelectionAction(state.selectionAction)) {
        state.selectionAction = mapped.selectionAction;
      }
      if (
        mapped.rightClickAction !== DEFAULT_TERMINAL_MOUSE_BINDINGS.right &&
        localStorage.getItem(TERMINAL_MOUSE_STORAGE_KEY) === null
      ) {
        localStorage.setItem(TERMINAL_MOUSE_STORAGE_KEY, JSON.stringify({ right: mapped.rightClickAction }));
      }
    }

    localStorage.setItem(TERMINAL_THEME_STORAGE_KEY, JSON.stringify(parsed));
  } catch {
    // Malformed localStorage JSON is ignored; stores fall back to defaults.
  }
}
