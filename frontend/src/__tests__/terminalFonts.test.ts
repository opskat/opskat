import { describe, expect, it } from "vitest";
import { resolveTerminalFontFamily, terminalFontPresets, withTerminalFontFallback } from "../data/terminalFonts";

describe("terminalFonts", () => {
  it("keeps font presets as static choices without system detection metadata", () => {
    expect(terminalFontPresets.some((preset) => "systemFontNames" in preset)).toBe(false);
  });

  it("keeps preset values as the primary font only", () => {
    expect(terminalFontPresets.find((preset) => preset.id === "fira-code")?.fontFamily).toBe("'Fira Code'");
  });

  it("uses the default font stack when the custom value is blank", () => {
    expect(resolveTerminalFontFamily("  ")).toBe("'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace");
  });

  it("keeps custom font family values unexpanded for storage", () => {
    expect(resolveTerminalFontFamily("Iosevka Term, monospace")).toBe("Iosevka Term, monospace");
  });

  it("adds shared fallbacks at terminal runtime without duplicating the primary font", () => {
    expect(withTerminalFontFallback("'Fira Code'")).toBe(
      "'Fira Code', 'JetBrains Mono', 'Cascadia Code', Menlo, monospace"
    );
  });

  it("strips trailing generic families before adding runtime fallbacks", () => {
    expect(withTerminalFontFallback("Iosevka Term, monospace")).toBe(
      "Iosevka Term, 'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace"
    );
  });

  it("uses the default runtime fallback when the runtime font value is blank", () => {
    expect(withTerminalFontFallback("  ")).toBe("'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace");
  });

  it("does not duplicate the default runtime fallback stack", () => {
    expect(resolveTerminalFontFamily("Iosevka Term, monospace")).toBe("Iosevka Term, monospace");
    expect(withTerminalFontFallback("'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace")).toBe(
      "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace"
    );
  });
});
