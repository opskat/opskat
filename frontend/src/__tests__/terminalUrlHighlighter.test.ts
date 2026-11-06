import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { attachTerminalUrlHighlighter } from "@/components/terminal/terminalUrlHighlighter";

type Cell = { chars: string; width: number };
function asciiLine(s: string, isWrapped = false) {
  const cells: Cell[] = [...s].map((ch) => ({ chars: ch, width: 1 }));
  return {
    isWrapped,
    length: cells.length,
    getCell: (x: number) => {
      const c = cells[x];
      return c ? { getWidth: () => c.width, getChars: () => c.chars } : undefined;
    },
  };
}

function makeTerm(opts: { lines: ReturnType<typeof asciiLine>[]; type?: "normal" | "alternate" }) {
  const listeners: Record<string, Array<() => void>> = { write: [], scroll: [], resize: [], buffer: [] };
  const registerMarker = vi.fn((offset: number) => ({ line: offset, dispose: vi.fn() }));
  const registerDecoration = vi.fn((o: Record<string, unknown>) => ({ ...o, dispose: vi.fn() }));
  const sub = (bucket: string) => (cb: () => void) => {
    listeners[bucket].push(cb);
    return { dispose: vi.fn() };
  };
  const term = {
    cols: 80,
    rows: 24,
    buffer: {
      active: {
        type: opts.type ?? "normal",
        viewportY: 0,
        baseY: 0,
        cursorY: 0,
        length: opts.lines.length,
        getLine: (y: number) => opts.lines[y],
      },
      onBufferChange: sub("buffer"),
    },
    onWriteParsed: sub("write"),
    onScroll: sub("scroll"),
    onResize: sub("resize"),
    registerMarker,
    registerDecoration,
  };
  const fire = (bucket: string) => listeners[bucket].forEach((cb) => cb());
  return { term, registerMarker, registerDecoration, fire };
}

beforeEach(() => {
  vi.stubGlobal("requestAnimationFrame", (cb: () => void) => {
    cb();
    return 1;
  });
  vi.stubGlobal("cancelAnimationFrame", () => {});
});
afterEach(() => vi.unstubAllGlobals());

describe("attachTerminalUrlHighlighter", () => {
  it("creates a decoration at the URL's columns when enabled", () => {
    const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
    const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
    expect(m.registerDecoration).toHaveBeenCalledTimes(1);
    expect(m.registerDecoration).toHaveBeenCalledWith(
      expect.objectContaining({ x: 3, width: 12, foregroundColor: "#1166ff", layer: "top" })
    );
    ctl.dispose();
  });

  it("does nothing in the alternate buffer", () => {
    const m = makeTerm({ lines: [asciiLine("go http://a.com")], type: "alternate" });
    attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
    expect(m.registerDecoration).not.toHaveBeenCalled();
  });

  it("does nothing when disabled, and creates on enable", () => {
    const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
    const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: false, color: "#1166ff" });
    expect(m.registerDecoration).not.toHaveBeenCalled();
    ctl.setEnabled(true);
    expect(m.registerDecoration).toHaveBeenCalledTimes(1);
  });

  it("ignores a non-#RRGGBB color", () => {
    const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
    attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "rgb(0,0,0)" });
    expect(m.registerDecoration).not.toHaveBeenCalled();
  });

  it("reconciles by diff: re-syncing identical content does not recreate decorations", () => {
    const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
    attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
    expect(m.registerDecoration).toHaveBeenCalledTimes(1);
    m.fire("write");
    m.fire("scroll");
    expect(m.registerDecoration).toHaveBeenCalledTimes(1); // unchanged -> not recreated
  });

  it("disposes all decorations and listeners on dispose", () => {
    const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
    const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
    const deco = m.registerDecoration.mock.results[0].value as { dispose: ReturnType<typeof vi.fn> };
    ctl.dispose();
    expect(deco.dispose).toHaveBeenCalled();
  });

  describe("inactive (hidden) terminal", () => {
    function scanCounter(m: ReturnType<typeof makeTerm>) {
      const buf = m.term.buffer.active;
      const orig = buf.getLine;
      const getLine = vi.fn(orig);
      buf.getLine = getLine;
      return getLine;
    }

    it("writes while inactive trigger no scan; reactivation scans the viewport once", () => {
      const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
      const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
      ctl.setActive(false);
      const scans = scanCounter(m);
      for (let i = 0; i < 50; i++) m.fire("write");
      m.fire("scroll");
      m.fire("resize");
      expect(scans).not.toHaveBeenCalled();
      ctl.setActive(true);
      expect(scans).toHaveBeenCalled();
      const after = scans.mock.calls.length;
      ctl.setActive(true);
      expect(scans.mock.calls.length).toBe(after);
    });

    it("URLs written while inactive are highlighted after activation", () => {
      const lines = [asciiLine("nothing")];
      const m = makeTerm({ lines });
      const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
      ctl.setActive(false);
      lines[0] = asciiLine("go http://b.com");
      m.fire("write");
      expect(m.registerDecoration).not.toHaveBeenCalled();
      ctl.setActive(true);
      expect(m.registerDecoration).toHaveBeenCalledTimes(1);
    });

    it("reactivation does not scan when nothing changed while hidden", () => {
      const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
      const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: true, color: "#1166ff" });
      ctl.setActive(false);
      const scans = scanCounter(m);
      ctl.setActive(true);
      expect(scans).not.toHaveBeenCalled();
    });

    it("setEnabled while inactive defers the scan until activation", () => {
      const m = makeTerm({ lines: [asciiLine("go http://a.com")] });
      const ctl = attachTerminalUrlHighlighter(m.term as never, { enabled: false, color: "#1166ff" });
      ctl.setActive(false);
      ctl.setEnabled(true);
      expect(m.registerDecoration).not.toHaveBeenCalled();
      ctl.setActive(true);
      expect(m.registerDecoration).toHaveBeenCalledTimes(1);
    });
  });
});
