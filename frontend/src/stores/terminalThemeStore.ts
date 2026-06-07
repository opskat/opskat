import { create } from "zustand";
import { persist } from "zustand/middleware";
import { TerminalTheme, builtinThemes } from "@/data/terminalThemes";
import {
  CUSTOM_TERMINAL_FONT_PRESET_ID,
  DEFAULT_TERMINAL_FONT_FAMILY,
  DEFAULT_TERMINAL_FONT_PRESET_ID,
  findTerminalFontPreset,
  normalizeTerminalFontFamily,
  quoteFamilyName,
  resolveTerminalFontFamily,
} from "@/data/terminalFonts";

export const SCROLLBACK_MIN = 100;
export const SCROLLBACK_MAX = 1000000;
export const SCROLLBACK_DEFAULT = 25000;

export type WebglFailureCause = "init-threw" | "context-loss";

export interface WebglFailure {
  cause: WebglFailureCause;
  name?: string;
  message: string;
  at: number;
}

interface TerminalThemeState {
  selectedThemeId: string;
  customThemes: TerminalTheme[];
  fontSize: number;
  fontPresetId: string;
  customFontFamily: string;
  fontFamily: string;
  scrollback: number;
  webglEnabled: boolean;
  // 最近一次 WebGL 自动关闭的原因。setWebglEnabled(true) 会把它清掉，所以只在
  // GPU 加速被系统自动关掉后到下一次用户主动开启之间存在。
  webglError: WebglFailure | null;

  setSelectedThemeId: (id: string) => void;
  setFontSize: (size: number) => void;
  setFontPresetId: (id: string) => void;
  setCustomFontFamily: (fontFamily: string) => void;
  setScrollback: (lines: number) => void;
  setWebglEnabled: (enabled: boolean) => void;
  reportWebglFailure: (failure: WebglFailure) => void;
  addCustomTheme: (theme: TerminalTheme) => void;
  updateCustomTheme: (theme: TerminalTheme) => void;
  removeCustomTheme: (id: string) => void;
  getActiveTheme: () => TerminalTheme;
}

function deriveFontFamily(fontPresetId: string, customFontFamily: string): string {
  if (fontPresetId === CUSTOM_TERMINAL_FONT_PRESET_ID) {
    return resolveTerminalFontFamily(customFontFamily);
  }
  if (fontPresetId === DEFAULT_TERMINAL_FONT_PRESET_ID) {
    return DEFAULT_TERMINAL_FONT_FAMILY;
  }
  const preset = findTerminalFontPreset(fontPresetId);
  if (preset) return preset.fontFamily;
  // System-font picker stores the family name itself as the preset id.
  const trimmed = normalizeTerminalFontFamily(fontPresetId);
  return trimmed ? quoteFamilyName(trimmed) : DEFAULT_TERMINAL_FONT_FAMILY;
}

export const useTerminalThemeStore = create<TerminalThemeState>()(
  persist(
    (set, get) => ({
      selectedThemeId: "default",
      customThemes: [],
      fontSize: 14,
      fontPresetId: DEFAULT_TERMINAL_FONT_PRESET_ID,
      customFontFamily: "",
      fontFamily: DEFAULT_TERMINAL_FONT_FAMILY,
      scrollback: SCROLLBACK_DEFAULT,
      webglEnabled: true,
      webglError: null,

      setSelectedThemeId: (id) => set({ selectedThemeId: id }),
      // 用户主动开启 WebGL → 清掉历史错误（不再显示红字提示）；关闭时保留现状
      // （手动关掉不该写入 webglError，但也不主动清——下次自动关掉的错误能继续展示）。
      setWebglEnabled: (enabled) => set(enabled ? { webglEnabled: true, webglError: null } : { webglEnabled: false }),
      reportWebglFailure: (failure) => set({ webglError: failure }),

      setFontSize: (size) => set({ fontSize: Math.max(8, Math.min(32, size)) }),

      setFontPresetId: (id) => {
        if (id === CUSTOM_TERMINAL_FONT_PRESET_ID) {
          const customFontFamily = normalizeTerminalFontFamily(get().customFontFamily);
          set({
            fontPresetId: CUSTOM_TERMINAL_FONT_PRESET_ID,
            customFontFamily,
            fontFamily: resolveTerminalFontFamily(customFontFamily),
          });
          return;
        }

        const preset = findTerminalFontPreset(id);
        if (preset) {
          set({ fontPresetId: preset.id, fontFamily: preset.fontFamily });
          return;
        }

        // Unknown id — treat as a system font family name picked from the
        // dynamic dropdown (where items use the family name itself as their id).
        // Blank input falls back to the default preset.
        const familyName = normalizeTerminalFontFamily(id);
        if (!familyName) {
          const def = findTerminalFontPreset(DEFAULT_TERMINAL_FONT_PRESET_ID);
          if (def) set({ fontPresetId: def.id, fontFamily: def.fontFamily });
          return;
        }
        set({
          fontPresetId: familyName,
          fontFamily: quoteFamilyName(familyName),
        });
      },

      setCustomFontFamily: (fontFamily) => {
        const customFontFamily = normalizeTerminalFontFamily(fontFamily);
        set({
          fontPresetId: CUSTOM_TERMINAL_FONT_PRESET_ID,
          customFontFamily,
          fontFamily: resolveTerminalFontFamily(customFontFamily),
        });
      },

      setScrollback: (lines) => {
        const n = Number.isFinite(lines) ? Math.floor(lines) : SCROLLBACK_DEFAULT;
        set({ scrollback: Math.max(SCROLLBACK_MIN, Math.min(SCROLLBACK_MAX, n)) });
      },

      addCustomTheme: (theme) =>
        set((state) => ({
          customThemes: [...state.customThemes, theme],
        })),

      updateCustomTheme: (theme) =>
        set((state) => ({
          customThemes: state.customThemes.map((t) => (t.id === theme.id ? theme : t)),
        })),

      removeCustomTheme: (id) =>
        set((state) => ({
          customThemes: state.customThemes.filter((t) => t.id !== id),
          // 如果删除的是当前选中的，回退到默认
          selectedThemeId: state.selectedThemeId === id ? "default" : state.selectedThemeId,
        })),

      getActiveTheme: () => {
        const { selectedThemeId, customThemes } = get();
        return (
          builtinThemes.find((t) => t.id === selectedThemeId) ||
          customThemes.find((t) => t.id === selectedThemeId) ||
          builtinThemes[0]
        );
      },
    }),
    {
      name: "terminal_theme",
      // fontFamily 是从 fontPresetId / customFontFamily 派生的字段,不要让它
      // 进入 localStorage —— 否则常量 DEFAULT_TERMINAL_FONT_FAMILY 一旦变更,
      // 老用户重新水合时 fontFamily 还指向旧字符串,withTerminalFontFallback
      // 短路条件失效,字体链就锁死在升级前的旧值上。
      partialize: ({ fontFamily: _omit, ...rest }) => rest,
      merge: (persistedState, currentState) => {
        const merged = { ...currentState, ...((persistedState as Partial<TerminalThemeState>) ?? {}) };
        merged.fontFamily = deriveFontFamily(merged.fontPresetId, merged.customFontFamily);
        return merged;
      },
    }
  )
);

/** 将 TerminalTheme 转换为 xterm.js ITheme 对象 */
export function toXtermTheme(theme: TerminalTheme) {
  return {
    background: theme.background,
    foreground: theme.foreground,
    cursor: theme.cursor,
    cursorAccent: theme.cursorAccent,
    selectionBackground: theme.selectionBackground,
    selectionForeground: theme.selectionForeground,
    selectionInactiveBackground: theme.selectionInactiveBackground,
    black: theme.black,
    red: theme.red,
    green: theme.green,
    yellow: theme.yellow,
    blue: theme.blue,
    magenta: theme.magenta,
    cyan: theme.cyan,
    white: theme.white,
    brightBlack: theme.brightBlack,
    brightRed: theme.brightRed,
    brightGreen: theme.brightGreen,
    brightYellow: theme.brightYellow,
    brightBlue: theme.brightBlue,
    brightMagenta: theme.brightMagenta,
    brightCyan: theme.brightCyan,
    brightWhite: theme.brightWhite,
  };
}
