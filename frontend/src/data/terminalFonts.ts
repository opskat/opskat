export interface TerminalFontPreset {
  id: string;
  name: string;
  fontFamily: string;
}

export const DEFAULT_TERMINAL_FONT_PRESET_ID = "default";
export const CUSTOM_TERMINAL_FONT_PRESET_ID = "custom";
export const DEFAULT_TERMINAL_FONT_FALLBACKS = [
  "'JetBrains Mono'",
  "'Fira Code'",
  "'Cascadia Code'",
  "Menlo",
  "monospace",
];
export const DEFAULT_TERMINAL_FONT_FAMILY = DEFAULT_TERMINAL_FONT_FALLBACKS.join(", ");
const TRAILING_GENERIC_FONT_FAMILY_RE = /(?:,\s*(?:ui-monospace|monospace|serif|sans-serif)\s*)+$/i;

export const terminalFontPresets: TerminalFontPreset[] = [
  {
    id: DEFAULT_TERMINAL_FONT_PRESET_ID,
    name: "Default",
    fontFamily: DEFAULT_TERMINAL_FONT_FAMILY,
  },
  {
    id: "jetbrains-mono",
    name: "JetBrains Mono",
    fontFamily: "'JetBrains Mono'",
  },
  {
    id: "fira-code",
    name: "Fira Code",
    fontFamily: "'Fira Code'",
  },
  {
    id: "cascadia-code",
    name: "Cascadia Code",
    fontFamily: "'Cascadia Code'",
  },
  {
    id: "sf-mono",
    name: "SF Mono",
    fontFamily: "'SF Mono'",
  },
  {
    id: "menlo",
    name: "Menlo",
    fontFamily: "Menlo",
  },
  {
    id: "monaco",
    name: "Monaco",
    fontFamily: "Monaco",
  },
  {
    id: "consolas",
    name: "Consolas",
    fontFamily: "Consolas",
  },
  {
    id: "source-code-pro",
    name: "Source Code Pro",
    fontFamily: "'Source Code Pro'",
  },
  {
    id: "hack",
    name: "Hack",
    fontFamily: "Hack",
  },
  {
    id: "ibm-plex-mono",
    name: "IBM Plex Mono",
    fontFamily: "'IBM Plex Mono'",
  },
  {
    id: "roboto-mono",
    name: "Roboto Mono",
    fontFamily: "'Roboto Mono'",
  },
  {
    id: "noto-sans-mono",
    name: "Noto Sans Mono",
    fontFamily: "'Noto Sans Mono'",
  },
  {
    id: "ubuntu-mono",
    name: "Ubuntu Mono",
    fontFamily: "'Ubuntu Mono'",
  },
  {
    id: "dejavu-sans-mono",
    name: "DejaVu Sans Mono",
    fontFamily: "'DejaVu Sans Mono'",
  },
];

export function normalizeTerminalFontFamily(fontFamily: string): string {
  return fontFamily.trim();
}

export function resolveTerminalFontFamily(fontFamily: string): string {
  const normalized = normalizeTerminalFontFamily(fontFamily);
  return normalized || DEFAULT_TERMINAL_FONT_FAMILY;
}

function splitFontFamilyList(fontFamily: string): string[] {
  const families: string[] = [];
  let current = "";
  let quote: string | undefined;

  for (const char of fontFamily) {
    if ((char === "'" || char === '"') && !quote) {
      quote = char;
      current += char;
      continue;
    }
    if (char === quote) {
      quote = undefined;
      current += char;
      continue;
    }
    if (char === "," && !quote) {
      const family = current.trim();
      if (family) families.push(family);
      current = "";
      continue;
    }
    current += char;
  }

  const family = current.trim();
  if (family) families.push(family);
  return families;
}

function normalizeFontFamilyToken(fontFamily: string): string {
  return fontFamily
    .trim()
    .replace(/^['"]|['"]$/g, "")
    .toLowerCase();
}

export function withTerminalFontFallback(fontFamily: string): string {
  const normalized = normalizeTerminalFontFamily(fontFamily);
  if (!normalized || normalized === DEFAULT_TERMINAL_FONT_FAMILY) return DEFAULT_TERMINAL_FONT_FAMILY;

  const primaryFontFamily = normalized.replace(TRAILING_GENERIC_FONT_FAMILY_RE, "").trim() || normalized;
  const primaryFonts = splitFontFamilyList(primaryFontFamily);
  const usedFontNames = new Set(primaryFonts.map(normalizeFontFamilyToken));
  const fallbackFonts = DEFAULT_TERMINAL_FONT_FALLBACKS.filter(
    (fallbackFont) => !usedFontNames.has(normalizeFontFamilyToken(fallbackFont))
  );

  return [...primaryFonts, ...fallbackFonts].join(", ");
}

export function findTerminalFontPreset(id: string): TerminalFontPreset | undefined {
  return terminalFontPresets.find((preset) => preset.id === id);
}
