import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const MAC_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36";

async function importMac() {
  Object.defineProperty(navigator, "userAgent", { value: MAC_UA, configurable: true });
  vi.resetModules();
  return import("../stores/shortcutStore");
}

function key(overrides: Partial<KeyboardEvent>): KeyboardEvent {
  return {
    code: "",
    metaKey: false,
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    ...overrides,
  } as KeyboardEvent;
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  delete (navigator as { userAgent?: string }).userAgent;
  vi.resetModules();
});

describe("matchShortcut on macOS", () => {
  it("treats mod as Cmd: Cmd+1 matches, Ctrl+1 does not", async () => {
    const { matchShortcut, DEFAULT_SHORTCUTS } = await importMac();
    const shortcuts = {
      ...DEFAULT_SHORTCUTS,
      "tab.1": { code: "Digit1", mod: true, ctrl: false, shift: false, alt: false },
    };

    expect(matchShortcut(key({ code: "Digit1", metaKey: true }), shortcuts)).toBe("tab.1");
    expect(matchShortcut(key({ code: "Digit1", ctrlKey: true }), shortcuts)).toBeNull();
  });

  it("treats ctrl as the Control key: Ctrl+1 matches, Cmd+1 does not", async () => {
    const { matchShortcut, DEFAULT_SHORTCUTS } = await importMac();
    const shortcuts = {
      ...DEFAULT_SHORTCUTS,
      "tab.1": { code: "Digit1", mod: false, ctrl: true, shift: false, alt: false },
    };

    expect(matchShortcut(key({ code: "Digit1", ctrlKey: true }), shortcuts)).toBe("tab.1");
    expect(matchShortcut(key({ code: "Digit1", metaKey: true }), shortcuts)).toBeNull();
  });

  it("supports a combined Cmd+Ctrl binding", async () => {
    const { matchShortcut, DEFAULT_SHORTCUTS } = await importMac();
    const shortcuts = {
      ...DEFAULT_SHORTCUTS,
      "tab.1": { code: "Digit1", mod: true, ctrl: true, shift: false, alt: false },
    };

    expect(matchShortcut(key({ code: "Digit1", metaKey: true, ctrlKey: true }), shortcuts)).toBe("tab.1");
    expect(matchShortcut(key({ code: "Digit1", metaKey: true }), shortcuts)).toBeNull();
  });

  it("does not treat Ctrl as no modifier", async () => {
    const { matchShortcut, DEFAULT_SHORTCUTS } = await importMac();
    const shortcuts = {
      ...DEFAULT_SHORTCUTS,
      "tab.close": { code: "KeyW", mod: false, ctrl: false, shift: false, alt: false },
    };

    expect(matchShortcut(key({ code: "KeyW", ctrlKey: true }), shortcuts)).toBeNull();
  });
});

describe("formatBinding on macOS", () => {
  it("renders the Control glyph for a ctrl binding", async () => {
    const { formatBinding } = await importMac();

    expect(formatBinding({ code: "Digit1", mod: false, ctrl: true, shift: false, alt: false })).toBe("⌃1");
  });

  it("orders modifiers as Control, Option, Shift, Command", async () => {
    const { formatBinding } = await importMac();

    expect(formatBinding({ code: "KeyD", mod: true, ctrl: true, shift: true, alt: true })).toBe("⌃⌥⇧⌘D");
  });
});

describe("legacy localStorage migration on macOS", () => {
  it("fills ctrl=false for bindings saved before the ctrl field existed", async () => {
    localStorage.setItem(
      "keyboard_shortcuts",
      JSON.stringify({ "tab.close": { code: "KeyQ", mod: true, shift: false, alt: false } })
    );

    const { useShortcutStore } = await importMac();

    expect(useShortcutStore.getState().shortcuts["tab.close"]).toEqual({
      code: "KeyQ",
      mod: true,
      ctrl: false,
      shift: false,
      alt: false,
    });
  });
});
