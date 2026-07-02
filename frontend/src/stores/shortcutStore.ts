import { create } from "zustand";

export const isMac = /Macintosh|Mac OS/.test(navigator.userAgent);

export interface ShortcutBinding {
  code: string; // KeyboardEvent.code, e.g. "Digit1", "KeyW", "BracketLeft"
  mod: boolean; // primary modifier: Cmd (Mac) / Ctrl (Win)
  ctrl: boolean; // Control key — Mac only (Win has no separate Cmd, so it's ignored there)
  shift: boolean;
  alt: boolean;
}

export type ShortcutAction =
  | "tab.1"
  | "tab.2"
  | "tab.3"
  | "tab.4"
  | "tab.5"
  | "tab.6"
  | "tab.7"
  | "tab.8"
  | "tab.9"
  | "tab.close"
  | "tab.prev"
  | "tab.next"
  | "split.vertical"
  | "split.horizontal"
  | "panel.ai"
  | "panel.sidebar"
  | "panel.switch"
  | "panel.filter"
  | "terminal.copy"
  | "terminal.paste"
  | "terminal.selectAll"
  | "terminal.find"
  | "page.home"
  | "page.settings"
  | "page.sshkeys"
  | "command.quickopen";

export const SHORTCUT_ACTIONS: ShortcutAction[] = [
  "tab.1",
  "tab.2",
  "tab.3",
  "tab.4",
  "tab.5",
  "tab.6",
  "tab.7",
  "tab.8",
  "tab.9",
  "tab.close",
  "tab.prev",
  "tab.next",
  "split.vertical",
  "split.horizontal",
  "panel.ai",
  "panel.sidebar",
  "panel.switch",
  "panel.filter",
  "terminal.copy",
  "terminal.paste",
  "terminal.selectAll",
  "terminal.find",
  "page.home",
  "page.settings",
  "page.sshkeys",
  "command.quickopen",
];

export const DEFAULT_SHORTCUTS: Record<ShortcutAction, ShortcutBinding> = {
  "tab.1": { code: "Digit1", mod: true, ctrl: false, shift: false, alt: false },
  "tab.2": { code: "Digit2", mod: true, ctrl: false, shift: false, alt: false },
  "tab.3": { code: "Digit3", mod: true, ctrl: false, shift: false, alt: false },
  "tab.4": { code: "Digit4", mod: true, ctrl: false, shift: false, alt: false },
  "tab.5": { code: "Digit5", mod: true, ctrl: false, shift: false, alt: false },
  "tab.6": { code: "Digit6", mod: true, ctrl: false, shift: false, alt: false },
  "tab.7": { code: "Digit7", mod: true, ctrl: false, shift: false, alt: false },
  "tab.8": { code: "Digit8", mod: true, ctrl: false, shift: false, alt: false },
  "tab.9": { code: "Digit9", mod: true, ctrl: false, shift: false, alt: false },
  "tab.close": { code: "KeyW", mod: true, ctrl: false, shift: false, alt: false },
  "tab.prev": { code: "BracketLeft", mod: true, ctrl: false, shift: true, alt: false },
  "tab.next": { code: "BracketRight", mod: true, ctrl: false, shift: true, alt: false },
  "split.vertical": { code: "KeyD", mod: true, ctrl: false, shift: false, alt: false },
  "split.horizontal": { code: "KeyD", mod: true, ctrl: false, shift: true, alt: false },
  "panel.ai": { code: "KeyB", mod: true, ctrl: false, shift: false, alt: false },
  "panel.sidebar": { code: "KeyE", mod: true, ctrl: false, shift: false, alt: false },
  "panel.switch": { code: "KeyE", mod: true, ctrl: false, shift: true, alt: false },
  "panel.filter": { code: "KeyF", mod: true, ctrl: false, shift: false, alt: false },
  "terminal.copy": { code: "KeyC", mod: true, ctrl: false, shift: !isMac, alt: false },
  "terminal.paste": { code: "KeyV", mod: true, ctrl: false, shift: !isMac, alt: false },
  "terminal.selectAll": { code: "KeyA", mod: true, ctrl: false, shift: false, alt: false },
  "terminal.find": { code: "KeyF", mod: true, ctrl: false, shift: false, alt: false },
  "page.home": { code: "KeyH", mod: true, ctrl: false, shift: true, alt: false },
  "page.settings": { code: "Comma", mod: true, ctrl: false, shift: false, alt: false },
  "page.sshkeys": { code: "KeyK", mod: true, ctrl: false, shift: true, alt: false },
  "command.quickopen": { code: "KeyP", mod: true, ctrl: false, shift: false, alt: false },
};

const STORAGE_KEY = "keyboard_shortcuts";
const DISABLE_IN_TERMINAL_KEY = "disable_shortcuts_in_terminal";

function loadDisableInTerminal(): boolean {
  try {
    return localStorage.getItem(DISABLE_IN_TERMINAL_KEY) === "true";
  } catch {
    return false;
  }
}

function saveDisableInTerminal(v: boolean) {
  localStorage.setItem(DISABLE_IN_TERMINAL_KEY, String(v));
}

function loadCustom(): Partial<Record<ShortcutAction, ShortcutBinding>> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Partial<Record<ShortcutAction, ShortcutBinding>>;
    // Normalize at the boundary: bindings persisted before `ctrl` existed lack the
    // field, so default it to false here rather than re-defaulting at every consumer.
    const normalized: Partial<Record<ShortcutAction, ShortcutBinding>> = {};
    for (const [action, binding] of Object.entries(parsed)) {
      normalized[action as ShortcutAction] = { ...binding, ctrl: binding.ctrl ?? false };
    }
    return normalized;
  } catch {
    return {};
  }
}

function saveCustom(shortcuts: Record<ShortcutAction, ShortcutBinding>) {
  const custom: Partial<Record<ShortcutAction, ShortcutBinding>> = {};
  for (const [key, val] of Object.entries(shortcuts)) {
    const def = DEFAULT_SHORTCUTS[key as ShortcutAction];
    if (
      def &&
      (val.code !== def.code ||
        val.mod !== def.mod ||
        val.ctrl !== def.ctrl ||
        val.shift !== def.shift ||
        val.alt !== def.alt)
    ) {
      custom[key as ShortcutAction] = val;
    }
  }
  if (Object.keys(custom).length > 0) {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(custom));
  } else {
    localStorage.removeItem(STORAGE_KEY);
  }
}

export function bindingsEqual(a: ShortcutBinding, b: ShortcutBinding): boolean {
  return a.code === b.code && a.mod === b.mod && a.ctrl === b.ctrl && a.shift === b.shift && a.alt === b.alt;
}

export function findShortcutConflict(
  action: ShortcutAction,
  binding: ShortcutBinding,
  shortcuts: Record<ShortcutAction, ShortcutBinding>
): ShortcutAction | null {
  for (const [candidateAction, candidateBinding] of Object.entries(shortcuts)) {
    if (candidateAction === action) continue;
    if (
      (action === "terminal.find" && candidateAction === "panel.filter") ||
      (action === "panel.filter" && candidateAction === "terminal.find")
    ) {
      continue;
    }
    if (bindingsEqual(binding, candidateBinding)) return candidateAction as ShortcutAction;
  }
  return null;
}

export function eventMatchesBinding(e: KeyboardEvent, binding: ShortcutBinding): boolean {
  if (e.code !== binding.code || e.shiftKey !== binding.shift || e.altKey !== binding.alt) return false;
  if (isMac) return e.metaKey === binding.mod && e.ctrlKey === binding.ctrl;
  return e.ctrlKey === binding.mod;
}

interface ShortcutState {
  shortcuts: Record<ShortcutAction, ShortcutBinding>;
  // When true, app shortcuts pass through to a focused terminal pane instead of
  // being handled here — so keystrokes reach the remote shell. Scoped to the
  // terminal only; shortcuts keep working everywhere else. Persisted, default off.
  disableShortcutsInTerminal: boolean;
  isRecording: boolean;
  setDisableShortcutsInTerminal: (v: boolean) => void;
  setIsRecording: (v: boolean) => void;
  updateShortcut: (action: ShortcutAction, binding: ShortcutBinding) => void;
  resetShortcut: (action: ShortcutAction) => void;
  resetAll: () => void;
  swapCmdCtrl: () => void;
}

export const useShortcutStore = create<ShortcutState>((set) => ({
  shortcuts: { ...DEFAULT_SHORTCUTS, ...loadCustom() },
  disableShortcutsInTerminal: loadDisableInTerminal(),
  isRecording: false,

  setDisableShortcutsInTerminal: (v) => {
    saveDisableInTerminal(v);
    set({ disableShortcutsInTerminal: v });
  },

  setIsRecording: (v) => set({ isRecording: v }),

  updateShortcut: (action, binding) => {
    set((state) => {
      const shortcuts = { ...state.shortcuts, [action]: binding };
      saveCustom(shortcuts);
      return { shortcuts };
    });
  },

  resetShortcut: (action) => {
    set((state) => {
      const shortcuts = {
        ...state.shortcuts,
        [action]: DEFAULT_SHORTCUTS[action],
      };
      saveCustom(shortcuts);
      return { shortcuts };
    });
  },

  resetAll: () => {
    localStorage.removeItem(STORAGE_KEY);
    set({ shortcuts: { ...DEFAULT_SHORTCUTS } });
  },

  // macOS only: exchange the Cmd (mod) and Ctrl flags on every binding, so the user
  // can flip the whole table between ⌘- and ⌃-based shortcuts with one click. The swap
  // is its own inverse, so calling it again restores the previous state. Has no sensible
  // meaning on Windows (no Cmd key), where the UI hides the trigger.
  swapCmdCtrl: () => {
    set((state) => {
      const shortcuts = {} as Record<ShortcutAction, ShortcutBinding>;
      for (const [action, binding] of Object.entries(state.shortcuts)) {
        shortcuts[action as ShortcutAction] = { ...binding, mod: binding.ctrl, ctrl: binding.mod };
      }
      saveCustom(shortcuts);
      return { shortcuts };
    });
  },
}));

// Match a KeyboardEvent against shortcuts
export function matchShortcut(
  e: KeyboardEvent,
  shortcuts: Record<ShortcutAction, ShortcutBinding>
): ShortcutAction | null {
  for (const [action, binding] of Object.entries(shortcuts)) {
    if (!eventMatchesBinding(e, binding)) continue;
    return action as ShortcutAction;
  }
  return null;
}

// Convert KeyboardEvent.code to display string
const CODE_DISPLAY: Record<string, string> = {
  Digit0: "0",
  Digit1: "1",
  Digit2: "2",
  Digit3: "3",
  Digit4: "4",
  Digit5: "5",
  Digit6: "6",
  Digit7: "7",
  Digit8: "8",
  Digit9: "9",
  BracketLeft: "[",
  BracketRight: "]",
  Minus: "-",
  Equal: "=",
  Backquote: "`",
  Comma: ",",
  Period: ".",
  Slash: "/",
  Backslash: "\\",
  Semicolon: ";",
  Quote: "'",
  Space: "Space",
  Enter: "Enter",
  Escape: "Esc",
  Backspace: "⌫",
  Tab: "Tab",
  Delete: "Del",
  ArrowUp: "↑",
  ArrowDown: "↓",
  ArrowLeft: "←",
  ArrowRight: "→",
};

function codeToDisplay(code: string): string {
  if (CODE_DISPLAY[code]) return CODE_DISPLAY[code];
  if (code.startsWith("Key")) return code.slice(3);
  return code;
}

export function formatBinding(binding: ShortcutBinding): string {
  const parts: string[] = [];
  if (isMac) {
    // Apple convention orders modifiers ⌃⌥⇧⌘ with the key last.
    if (binding.ctrl) parts.push("⌃");
    if (binding.alt) parts.push("⌥");
    if (binding.shift) parts.push("⇧");
    if (binding.mod) parts.push("⌘");
    parts.push(codeToDisplay(binding.code));
    return parts.join("");
  }
  if (binding.mod) parts.push("Ctrl");
  if (binding.shift) parts.push("Shift");
  if (binding.alt) parts.push("Alt");
  parts.push(codeToDisplay(binding.code));
  return parts.join("+");
}

// Platform-convention modifier shortcut (Cmd on Mac, Ctrl elsewhere) — for
// system-level bindings NOT in the shortcut registry (browser/xterm copy-paste,
// editor shortcuts, etc.). User-configurable shortcuts must use
// formatBinding(shortcuts[action]) so the UI follows rebindings.
export function formatModKey(code: string, options: { shift?: boolean; alt?: boolean } = {}): string {
  return formatBinding({ code, mod: true, ctrl: false, shift: options.shift ?? false, alt: options.alt ?? false });
}
