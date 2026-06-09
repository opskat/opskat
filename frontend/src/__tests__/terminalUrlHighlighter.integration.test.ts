import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { Terminal } from "@xterm/xterm";
import { attachTerminalUrlHighlighter } from "@/components/terminal/terminalUrlHighlighter";

// Run rAF synchronously so the highlighter's debounced sync() executes inline.
beforeEach(() => {
  vi.stubGlobal("requestAnimationFrame", (cb: (t: number) => void) => {
    cb(0);
    return 1;
  });
  vi.stubGlobal("cancelAnimationFrame", () => {});
});
afterEach(() => vi.unstubAllGlobals());

function decorationCount(term: Terminal): number {
  const svc = (term as unknown as { _core: { _decorationService: { decorations: Iterable<unknown> } } })._core
    ._decorationService.decorations;
  return [...svc].length;
}

describe("terminalUrlHighlighter against real @xterm/xterm", () => {
  it("ROOT CAUSE: registerDecoration throws without allowProposedApi", () => {
    const term = new Terminal({ cols: 80, rows: 24 });
    const marker = term.registerMarker(0); // registerMarker is NOT gated
    expect(() => term.registerDecoration({ marker: marker!, foregroundColor: "#89b4fa" })).toThrow(/allowProposedApi/);
    term.dispose();
  });

  it("creates a real decoration for an on-screen URL when allowProposedApi is set", async () => {
    const term = new Terminal({ cols: 80, rows: 24, allowProposedApi: true });
    await new Promise<void>((resolve) => term.write("see https://example.com\r\n", () => resolve()));
    const ctl = attachTerminalUrlHighlighter(term, { enabled: true, color: "#89b4fa" });
    expect(decorationCount(term)).toBeGreaterThan(0);
    ctl.dispose();
    term.dispose();
  });
});
