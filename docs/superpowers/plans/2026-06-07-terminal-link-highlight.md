# 终端链接高亮开关 (#153) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persisted, default-off "Highlight Links" terminal setting that tints `http(s)://` URLs in the normal buffer with the theme link color, via a pure xterm decoration overlay.

**Architecture:** A new pure module (`terminalUrlScan.ts`) detects URLs and computes *real terminal columns* (cell-width aware). A controller (`terminalUrlHighlighter.ts`) maintains decorations for only the visible window, reconciled by diff (no buffer mutation, no full-rescan churn). `terminalRegistry.ts` attaches/disposes it; `Terminal.tsx` pushes the enabled flag + theme link color; the store holds the toggle; the settings panel renders a `Switch`.

**Tech Stack:** React 19, Zustand (persist), @xterm/xterm 6, Vitest.

**Why this shape (root causes of the #153 revert):** the reverted code ran two competing mechanisms (decorations *and* ANSI injection), rebuilt all decorations on every event (the "刷新" churn), and used JS string indices as decoration columns (wrong with CJK/wide chars). This plan: one mechanism, diff reconcile of the visible window only, and cell-accurate columns with a CJK regression test.

**Out of scope / accepted limitations:** alt-buffer (full-screen TUI) URLs are not highlighted — `registerDecoration` returns `undefined` there. Clicking behavior is unchanged (`WebLinksAddon` stays always-on).

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `frontend/src/components/terminal/terminalUrlScan.ts` | Create | Pure: URL detection + column geometry + per-row segments for wrapped URLs. Owns `normalizeHttpUrl`. |
| `frontend/src/components/terminal/terminalUrlHighlighter.ts` | Create | Decoration overlay controller (attach/setEnabled/setColor/dispose). |
| `frontend/src/__tests__/terminalUrlScan.test.ts` | Create | Unit tests for scan (incl. CJK + wrapped). |
| `frontend/src/__tests__/terminalUrlHighlighter.test.ts` | Create | Unit tests for the controller. |
| `frontend/src/stores/terminalThemeStore.ts` | Modify | `highlightLinks` state + `setHighlightLinks`. |
| `frontend/src/__tests__/terminalThemeStore.test.ts` | Modify | Default + setter test. |
| `frontend/src/components/terminal/terminalRegistry.ts` | Modify | Attach/dispose highlighter; `terminalUrlHighlightColor`; import `normalizeHttpUrl` from scan. |
| `frontend/src/__tests__/terminalRegistry.test.ts` | Modify | Mock highlighter; assert attach + dispose. |
| `frontend/src/components/terminal/Terminal.tsx` | Modify | Pass `highlightLinks` to init; push enabled/color in theme effect. |
| `frontend/src/components/settings/AppearanceSection.tsx` | Modify | `Switch` row. |
| `frontend/src/i18n/locales/en/common.json` | Modify | `terminal.highlightLinks` + hint. |
| `frontend/src/i18n/locales/zh-CN/common.json` | Modify | `terminal.highlightLinks` + hint. |

All commands run from `frontend/`. Commit messages end with `#153` (repo convention).

---

### Task 1: Store — `highlightLinks` toggle

**Files:**
- Modify: `frontend/src/stores/terminalThemeStore.ts`
- Test: `frontend/src/__tests__/terminalThemeStore.test.ts`

- [ ] **Step 1: Write the failing test**

In `frontend/src/__tests__/terminalThemeStore.test.ts`, find the `describe("setWebglEnabled"...)` block (or any existing `describe` inside the top-level suite) and add a sibling block:

```ts
  describe("setHighlightLinks", () => {
    it("defaults to disabled and toggles URL highlighting", () => {
      expect(useTerminalThemeStore.getState().highlightLinks).toBe(false);

      useTerminalThemeStore.getState().setHighlightLinks(true);
      expect(useTerminalThemeStore.getState().highlightLinks).toBe(true);

      useTerminalThemeStore.getState().setHighlightLinks(false);
      expect(useTerminalThemeStore.getState().highlightLinks).toBe(false);
    });
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/__tests__/terminalThemeStore.test.ts -t "setHighlightLinks"`
Expected: FAIL — `setHighlightLinks is not a function` / `highlightLinks` is `undefined`.

- [ ] **Step 3: Implement**

In `frontend/src/stores/terminalThemeStore.ts`:

Add to the `TerminalThemeState` interface, right after `webglEnabled: boolean;`:

```ts
  highlightLinks: boolean;
```

Add to the actions in the interface, right after `setWebglEnabled: (enabled: boolean) => void;`:

```ts
  setHighlightLinks: (enabled: boolean) => void;
```

Add to the initial state object, right after `webglEnabled: true,`:

```ts
      highlightLinks: false,
```

Add the setter, right after the `setWebglEnabled: ...` line:

```ts
      setHighlightLinks: (enabled) => set({ highlightLinks: enabled }),
```

(No `partialize` change needed: it persists everything except `fontFamily`, so `highlightLinks` is persisted automatically.)

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/__tests__/terminalThemeStore.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/stores/terminalThemeStore.ts src/__tests__/terminalThemeStore.test.ts
git commit -m "✨ 终端主题 store 增加 highlightLinks 开关 #153"
```

---

### Task 2: `terminalUrlScan.ts` — URL detection + column geometry

**Files:**
- Create: `frontend/src/components/terminal/terminalUrlScan.ts`
- Test: `frontend/src/__tests__/terminalUrlScan.test.ts`

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/terminalUrlScan.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { findUrlRowSpans, normalizeHttpUrl } from "@/components/terminal/terminalUrlScan";

type Cell = { chars: string; width: number };

function makeLine(cells: Cell[], isWrapped = false) {
  return {
    isWrapped,
    length: cells.length,
    getCell: (x: number) => {
      const c = cells[x];
      if (!c) return undefined;
      return { getWidth: () => c.width, getChars: () => c.chars };
    },
  };
}

function asciiLine(s: string, isWrapped = false) {
  return makeLine([...s].map((ch) => ({ chars: ch, width: 1 })), isWrapped);
}

function wide(ch: string): Cell[] {
  // A width-2 glyph occupies two cells: the glyph cell + a width-0 spacer.
  return [{ chars: ch, width: 2 }, { chars: "", width: 0 }];
}

function makeBuffer(lines: ReturnType<typeof makeLine>[]) {
  return { length: lines.length, getLine: (y: number) => lines[y] };
}

describe("normalizeHttpUrl", () => {
  it("accepts http/https and rejects others", () => {
    expect(normalizeHttpUrl("http://a.com")).toBe("http://a.com");
    expect(normalizeHttpUrl("https://a.com/x")).toBe("https://a.com/x");
    expect(normalizeHttpUrl("ftp://a.com")).toBeUndefined();
    expect(normalizeHttpUrl("not a url")).toBeUndefined();
  });
});

describe("findUrlRowSpans", () => {
  it("finds a single URL with correct columns on an ascii line", () => {
    const buf = makeBuffer([asciiLine("see http://a.com x")]);
    const spans = findUrlRowSpans(buf, 0, 0, 80);
    expect(spans).toEqual([{ line: 0, startCol: 4, width: 12, url: "http://a.com" }]);
  });

  it("trims trailing punctuation so highlight matches click span", () => {
    const buf = makeBuffer([asciiLine("visit http://a.com.")]);
    const spans = findUrlRowSpans(buf, 0, 0, 80);
    expect(spans).toEqual([{ line: 0, startCol: 6, width: 12, url: "http://a.com" }]);
  });

  it("ignores non-http tokens", () => {
    const buf = makeBuffer([asciiLine("ftp://a.com and plain text")]);
    expect(findUrlRowSpans(buf, 0, 0, 80)).toEqual([]);
  });

  it("computes real columns when a wide CJK char precedes the URL", () => {
    // "你" is 2 columns; the URL must start at column 2, not string index 1.
    const buf = makeBuffer([
      makeLine([...wide("你"), ...[..."http://a.com"].map((c) => ({ chars: c, width: 1 }))]),
    ]);
    const spans = findUrlRowSpans(buf, 0, 0, 80);
    expect(spans).toEqual([{ line: 0, startCol: 2, width: 12, url: "http://a.com" }]);
  });

  it("emits per-row segments for a URL wrapped across two rows", () => {
    const buf = makeBuffer([asciiLine("see http://exa"), asciiLine("mple.com/x", true)]);
    const spans = findUrlRowSpans(buf, 0, 1, 80);
    expect(spans).toEqual([
      { line: 0, startCol: 4, width: 10, url: "http://example.com/x" },
      { line: 1, startCol: 0, width: 10, url: "http://example.com/x" },
    ]);
  });

  it("walks up to the logical line start when the window opens on a wrapped row", () => {
    const buf = makeBuffer([asciiLine("see http://exa"), asciiLine("mple.com/x", true)]);
    const spans = findUrlRowSpans(buf, 1, 1, 80);
    expect(spans).toEqual([
      { line: 0, startCol: 4, width: 10, url: "http://example.com/x" },
      { line: 1, startCol: 0, width: 10, url: "http://example.com/x" },
    ]);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/__tests__/terminalUrlScan.test.ts`
Expected: FAIL — cannot resolve `@/components/terminal/terminalUrlScan`.

- [ ] **Step 3: Implement**

Create `frontend/src/components/terminal/terminalUrlScan.ts`:

```ts
// Pure URL detection + terminal-column geometry for the link highlighter.
// No xterm side effects so it can be unit-tested in isolation. Owns the URL
// rules (regex / trailing-punctuation trim / normalizeHttpUrl) so that the
// highlight span is, by construction, the same span the WebLinksAddon click
// handler uses (it imports normalizeHttpUrl from here).

const HTTP_URL_PATTERN = /https?:\/\/[^\s<>"'`]+/gi;
const TRAILING_URL_PUNCTUATION = /[),.;!?\]}]+$/;

/** Minimal structural subset of xterm's IBufferCell we depend on. */
interface ScanCell {
  getWidth(): number;
  getChars(): string;
}

/** Minimal structural subset of xterm's IBufferLine. */
interface ScanLine {
  readonly isWrapped: boolean;
  readonly length: number;
  getCell(x: number): ScanCell | undefined;
}

/** Minimal structural subset of xterm's IBuffer. */
interface ScanBuffer {
  readonly length: number;
  getLine(y: number): ScanLine | undefined;
}

/** A URL occurrence on a single physical terminal row, in terminal columns. */
export interface TerminalUrlRowSpan {
  /** Absolute buffer line index of the physical row. */
  line: number;
  /** Start column (0-based) of the highlighted segment on this row. */
  startCol: number;
  /** Width of the segment in cells. */
  width: number;
  /** The normalized URL (whole URL, identical across the wrapped row segments). */
  url: string;
}

/** Validate an http(s) URL; returns it unchanged if valid, else undefined. */
export function normalizeHttpUrl(url: string): string | undefined {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
    return url;
  } catch {
    return undefined;
  }
}

function trimTrailingPunctuation(url: string): string {
  return url.replace(TRAILING_URL_PUNCTUATION, "");
}

// Build the display text of one physical row plus a column map. colStarts has
// text.length + 1 entries: colStarts[i] is the terminal column where text's
// i-th code unit begins, and colStarts[text.length] is the column just past the
// last non-zero-width cell (the sentinel used to size the final char).
function rowTextWithColumns(line: ScanLine, cols: number): { text: string; colStarts: number[] } {
  let text = "";
  const colStarts: number[] = [];
  let nextCol = 0;
  const max = Math.min(line.length, cols);
  for (let x = 0; x < max; x++) {
    const cell = line.getCell(x);
    if (!cell) break;
    const w = cell.getWidth();
    if (w === 0) continue; // spacer cell of a preceding wide char
    const chars = cell.getChars() || " ";
    for (let k = 0; k < chars.length; k++) {
      text += chars[k];
      colStarts.push(x); // one entry per code unit, aligned with `text`
    }
    nextCol = x + w;
  }
  colStarts.push(nextCol);
  return { text, colStarts };
}

/**
 * Find http(s) URLs in the buffer rows [startLine, endLine] (inclusive),
 * returning one span per physical row a URL occupies. Wrapped URLs (a URL long
 * enough to span `isWrapped` continuation rows) are joined into a logical line,
 * matched whole, then split back into per-row segments. If the window opens on a
 * wrapped continuation row, the scan walks up to the logical line start so a URL
 * scrolling in from above is fully covered.
 */
export function findUrlRowSpans(
  buffer: ScanBuffer,
  startLine: number,
  endLine: number,
  cols: number
): TerminalUrlRowSpan[] {
  const spans: TerminalUrlRowSpan[] = [];
  if (buffer.length === 0) return spans;

  let logicalStart = Math.max(0, Math.min(startLine, buffer.length - 1));
  while (logicalStart > 0 && buffer.getLine(logicalStart)?.isWrapped) logicalStart--;

  let y = logicalStart;
  while (y <= endLine && y < buffer.length) {
    // Assemble one logical line: row y + following isWrapped rows.
    const rowLines: number[] = [];
    const charRow: number[] = []; // logical char index -> row index in rowLines
    const charStartCol: number[] = [];
    const charEndCol: number[] = [];
    let logicalText = "";
    let yy = y;
    do {
      const line = buffer.getLine(yy);
      if (!line) break;
      const { text, colStarts } = rowTextWithColumns(line, cols);
      const rowIdx = rowLines.length;
      rowLines.push(yy);
      for (let k = 0; k < text.length; k++) {
        logicalText += text[k];
        charRow.push(rowIdx);
        charStartCol.push(colStarts[k]);
        charEndCol.push(colStarts[k + 1]);
      }
      yy++;
    } while (yy < buffer.length && buffer.getLine(yy)?.isWrapped === true);

    for (const match of logicalText.matchAll(HTTP_URL_PATTERN)) {
      const url = trimTrailingPunctuation(match[0]);
      if (!normalizeHttpUrl(url)) continue;
      const startIdx = match.index ?? 0;
      const endIdx = startIdx + url.length; // exclusive, in logical code units
      let i = startIdx;
      while (i < endIdx) {
        const rowIdx = charRow[i];
        let j = i + 1;
        while (j < endIdx && charRow[j] === rowIdx) j++;
        const segStartCol = charStartCol[i];
        const segEndCol = charEndCol[j - 1];
        spans.push({ line: rowLines[rowIdx], startCol: segStartCol, width: segEndCol - segStartCol, url });
        i = j;
      }
    }

    y = yy; // skip past the wrapped continuation rows we already consumed
  }
  return spans;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/__tests__/terminalUrlScan.test.ts`
Expected: PASS (all 7 tests).

- [ ] **Step 5: Commit**

```bash
git add src/components/terminal/terminalUrlScan.ts src/__tests__/terminalUrlScan.test.ts
git commit -m "✨ 终端 URL 扫描:列几何与换行分段 #153"
```

---

### Task 3: `terminalUrlHighlighter.ts` — decoration overlay controller

**Files:**
- Create: `frontend/src/components/terminal/terminalUrlHighlighter.ts`
- Test: `frontend/src/__tests__/terminalUrlHighlighter.test.ts`

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/terminalUrlHighlighter.test.ts`:

```ts
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
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/__tests__/terminalUrlHighlighter.test.ts`
Expected: FAIL — cannot resolve `@/components/terminal/terminalUrlHighlighter`.

- [ ] **Step 3: Implement**

Create `frontend/src/components/terminal/terminalUrlHighlighter.ts`:

```ts
import type { Terminal, IDecoration, IMarker } from "@xterm/xterm";
import { findUrlRowSpans } from "./terminalUrlScan";

const HEX_COLOR_PATTERN = /^#[0-9a-f]{6}$/i;

export interface TerminalUrlHighlighterController {
  setEnabled(enabled: boolean): void;
  setColor(color: string | undefined): void;
  dispose(): void;
}

function validColor(color: string | undefined): string | undefined {
  if (!color) return undefined;
  // registerDecoration.foregroundColor only supports #RRGGBB.
  return HEX_COLOR_PATTERN.test(color) ? color : undefined;
}

/**
 * Attach a URL highlight overlay to a terminal. The buffer is never mutated:
 * URLs are tinted by xterm decorations computed from the *visible window* and
 * reconciled by diff (create missing, dispose stale) — never a full rebuild, so
 * there is no flicker/churn. Off-screen and scrollback URLs are highlighted
 * lazily as they scroll into view. Does nothing in the alternate buffer.
 */
export function attachTerminalUrlHighlighter(
  term: Terminal,
  init: { enabled: boolean; color: string | undefined }
): TerminalUrlHighlighterController {
  let enabled = init.enabled;
  let color = validColor(init.color);
  const decorations = new Map<string, { marker: IMarker; decoration: IDecoration }>();
  let rafId = 0;
  let disposed = false;

  const clearAll = () => {
    for (const { marker, decoration } of decorations.values()) {
      decoration.dispose();
      marker.dispose();
    }
    decorations.clear();
  };

  const sync = () => {
    if (disposed) return;
    const buffer = term.buffer.active;
    if (!enabled || !color || buffer.type === "alternate") {
      clearAll();
      return;
    }
    const top = Math.max(0, buffer.viewportY);
    const bottom = Math.min(buffer.length - 1, buffer.viewportY + term.rows - 1);
    const spans = findUrlRowSpans(buffer, top, bottom, term.cols);

    const desired = new Map<string, { line: number; startCol: number; width: number }>();
    for (const s of spans) {
      const key = `${s.line}:${s.startCol}:${s.width}:${s.url}`;
      if (!desired.has(key)) desired.set(key, { line: s.line, startCol: s.startCol, width: s.width });
    }

    for (const [key, entry] of decorations) {
      if (!desired.has(key)) {
        entry.decoration.dispose();
        entry.marker.dispose();
        decorations.delete(key);
      }
    }

    const anchor = buffer.baseY + buffer.cursorY;
    for (const [key, d] of desired) {
      if (decorations.has(key)) continue;
      const marker = term.registerMarker(d.line - anchor);
      if (!marker) continue;
      const decoration = term.registerDecoration({
        marker,
        x: d.startCol,
        width: d.width,
        foregroundColor: color,
        layer: "top",
      });
      if (!decoration) {
        marker.dispose();
        continue;
      }
      decorations.set(key, { marker, decoration });
    }
  };

  const scheduleSync = () => {
    if (disposed || rafId) return;
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      sync();
    });
  };

  const subs = [
    term.onWriteParsed(scheduleSync),
    term.onScroll(scheduleSync),
    term.onResize(scheduleSync),
    term.buffer.onBufferChange(scheduleSync),
  ];

  scheduleSync(); // initial paint

  return {
    setEnabled(next) {
      if (enabled === next) return;
      enabled = next;
      scheduleSync();
    },
    setColor(next) {
      const v = validColor(next);
      if (v === color) return;
      color = v;
      clearAll(); // existing decorations carry the old color; rebuild on next sync
      scheduleSync();
    },
    dispose() {
      disposed = true;
      if (rafId) cancelAnimationFrame(rafId);
      for (const s of subs) s.dispose();
      clearAll();
    },
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/__tests__/terminalUrlHighlighter.test.ts`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add src/components/terminal/terminalUrlHighlighter.ts src/__tests__/terminalUrlHighlighter.test.ts
git commit -m "✨ 终端 URL 高亮 decoration overlay 控制器 #153"
```

---

### Task 4: Wire highlighter into `terminalRegistry.ts`

**Files:**
- Modify: `frontend/src/components/terminal/terminalRegistry.ts`
- Test: `frontend/src/__tests__/terminalRegistry.test.ts`

- [ ] **Step 1: Write the failing test**

In `frontend/src/__tests__/terminalRegistry.test.ts`, add spies to the `vi.hoisted(...)` return object (alongside the other `*Spy` fields, e.g. after `webLinksAddonDisposeSpy`):

```ts
    attachUrlHighlighterSpy: vi.fn(),
    urlHighlighterDisposeSpy: vi.fn(),
```

Add a module mock next to the other `vi.mock(...)` calls (e.g. right after the `@xterm/addon-webgl` mock):

```ts
vi.mock("@/components/terminal/terminalUrlHighlighter", () => ({
  attachTerminalUrlHighlighter: (...args: unknown[]) => {
    hoisted.attachUrlHighlighterSpy(...args);
    return {
      setEnabled: vi.fn(),
      setColor: vi.fn(),
      dispose: () => {
        hoisted.disposeOrder.push("urlHighlighter");
        hoisted.urlHighlighterDisposeSpy();
      },
    };
  },
}));
```

Add a test inside the main `describe(...)`, after one of the existing creation tests. `getOrCreateTerminal` and `disposeTerminal` are already imported in this file (line ~240), and the file's creation pattern is `getOrCreateTerminal("sess-x", { fontSize: 14, fontFamily: "mono", scrollback: 1000 })`:

```ts
  it("attaches a url highlighter and disposes it with the terminal", () => {
    getOrCreateTerminal("sess-highlight", { fontSize: 14, fontFamily: "mono", scrollback: 1000 });
    expect(hoisted.attachUrlHighlighterSpy).toHaveBeenCalledTimes(1);

    disposeTerminal("sess-highlight");
    expect(hoisted.urlHighlighterDisposeSpy).toHaveBeenCalledTimes(1);
  });
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/__tests__/terminalRegistry.test.ts -t "url highlighter"`
Expected: FAIL — `attachUrlHighlighterSpy` not called (highlighter not yet attached).

- [ ] **Step 3: Implement**

In `frontend/src/components/terminal/terminalRegistry.ts`:

Add imports near the other terminal-local imports (after the `attachXtermRolloverGuard` import):

```ts
import { attachTerminalUrlHighlighter, type TerminalUrlHighlighterController } from "./terminalUrlHighlighter";
import { normalizeHttpUrl } from "./terminalUrlScan";
```

Remove the local `normalizeHttpUrl` function at the bottom of the file (the `function normalizeHttpUrl(url: string): string | undefined { ... }` block) — it now lives in `terminalUrlScan.ts`.

Add `urlHighlighter` to the `TerminalInstance` interface, after `bridge: TerminalInputBridge;`:

```ts
  urlHighlighter: TerminalUrlHighlighterController;
```

Add `highlightLinks?: boolean;` to the `init` parameter type of `getOrCreateTerminal`, after `webglEnabled?: boolean;`:

```ts
    highlightLinks?: boolean;
```

Immediately after `term.open(container);`, add:

```ts
  const urlHighlighter = attachTerminalUrlHighlighter(term, {
    enabled: init.highlightLinks === true,
    color: terminalUrlHighlightColor(init.theme),
  });
```

Add `urlHighlighter` to the `instance` object literal, after `bridge,`:

```ts
    urlHighlighter,
```

In `instance.dispose`, dispose it right after `bridge.dispose();`:

```ts
      urlHighlighter.dispose();
```

Add the exported color helper near `getTerminalInstance` (e.g. just after it):

```ts
export function terminalUrlHighlightColor(theme: ITheme | undefined): string | undefined {
  return theme?.brightBlue ?? theme?.blue;
}
```

(`ITheme` is already imported at the top: `import { Terminal as XTerminal, type ITheme } from "@xterm/xterm";`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `npx vitest run src/__tests__/terminalRegistry.test.ts`
Expected: PASS (existing tests + the new one). The WebLinks click handler still works because it now imports `normalizeHttpUrl` from `terminalUrlScan`.

- [ ] **Step 5: Commit**

```bash
git add src/components/terminal/terminalRegistry.ts src/__tests__/terminalRegistry.test.ts
git commit -m "✨ terminalRegistry 挂载 URL 高亮 overlay #153"
```

---

### Task 5: Push enabled + color from `Terminal.tsx`

**Files:**
- Modify: `frontend/src/components/terminal/Terminal.tsx`

(No new test: this is wiring covered by the controller/registry tests; verified by build + manual run in Task 7.)

- [ ] **Step 1: Implement**

In `frontend/src/components/terminal/Terminal.tsx`:

Update the registry import to also bring in the color helper (the existing import is `import { getOrCreateTerminal, getTerminalInstance } from "./terminalRegistry";`):

```ts
import { getOrCreateTerminal, getTerminalInstance, terminalUrlHighlightColor } from "./terminalRegistry";
```

Read the toggle from the store, next to the other `useTerminalThemeStore` selectors (after the `webglEnabled` selector):

```ts
  const highlightLinks = useTerminalThemeStore((s) => s.highlightLinks);
```

Pass it into the `getOrCreateTerminal(sessionId, { ... })` init object (after `webglEnabled,`):

```ts
      highlightLinks,
```

In the theme effect (the `useEffect` whose deps are `[xtermTheme, fontSize, fontFamily, scrollback]`), before `fitAddonRef.current?.fit();`, add:

```ts
    const inst = getTerminalInstance(sessionId);
    inst?.urlHighlighter.setEnabled(highlightLinks);
    inst?.urlHighlighter.setColor(terminalUrlHighlightColor(xtermTheme));
```

And add `highlightLinks` to that effect's dependency array:

```ts
  }, [xtermTheme, fontSize, fontFamily, scrollback, highlightLinks]);
```

- [ ] **Step 2: Verify type-check / lint**

Run: `npx eslint src/components/terminal/Terminal.tsx`
Expected: no errors (the `eslint-disable-next-line react-hooks/exhaustive-deps` above the deps array stays; `highlightLinks` is now listed).

- [ ] **Step 3: Commit**

```bash
git add src/components/terminal/Terminal.tsx
git commit -m "✨ Terminal 组件联动链接高亮开关与主题色 #153"
```

---

### Task 6: Settings `Switch` + i18n

**Files:**
- Modify: `frontend/src/components/settings/AppearanceSection.tsx`
- Modify: `frontend/src/i18n/locales/en/common.json`
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`

- [ ] **Step 1: Implement i18n (en)**

In `frontend/src/i18n/locales/en/common.json`, after the line:

```json
    "gpuAccelerationErrorContextLoss": "WebGL renderer was auto-disabled because the GPU context was lost.",
```

insert:

```json
    "highlightLinks": "Highlight Links",
    "highlightLinksHint": "Keep terminal URLs tinted with the theme link color. When off, links are only underlined on hover.",
```

- [ ] **Step 2: Implement i18n (zh-CN)**

In `frontend/src/i18n/locales/zh-CN/common.json`, after the line:

```json
    "gpuAccelerationErrorContextLoss": "WebGL 渲染器上下文丢失，GPU 加速已被自动关闭",
```

insert:

```json
    "highlightLinks": "高亮链接",
    "highlightLinksHint": "打开后，终端中的 URL 会以主题蓝色常驻显示；关闭时仅在鼠标悬停时显示下划线",
```

- [ ] **Step 3: Implement the Switch row**

In `frontend/src/components/settings/AppearanceSection.tsx`, add `highlightLinks` and `setHighlightLinks` to the destructured `useTerminalThemeStore()` (after `setWebglEnabled,`):

```ts
    highlightLinks,
    setHighlightLinks,
```

Then, between the GPU-acceleration block's closing `</div>` (the one right before `<Separator />`) and that `<Separator />`, insert:

```tsx
          <div className="flex items-start justify-between gap-4">
            <div className="grid gap-1">
              <Label>{t("terminal.highlightLinks")}</Label>
              <p className="text-xs text-muted-foreground">{t("terminal.highlightLinksHint")}</p>
            </div>
            <Switch checked={highlightLinks} onCheckedChange={setHighlightLinks} />
          </div>

```

(`Label` and `Switch` are already imported in this file.)

- [ ] **Step 4: Verify**

Run: `npx eslint src/components/settings/AppearanceSection.tsx && node --check /dev/stdin <<<'JSON.parse(require("fs").readFileSync("src/i18n/locales/en/common.json"));JSON.parse(require("fs").readFileSync("src/i18n/locales/zh-CN/common.json"))'`
Expected: no eslint errors; JSON parses (no trailing-comma/syntax error).

If the heredoc form is awkward in your shell, instead run: `npx vitest run` (the i18n JSON is imported by the app/tests and will fail to parse if malformed).

- [ ] **Step 5: Commit**

```bash
git add src/components/settings/AppearanceSection.tsx src/i18n/locales/en/common.json src/i18n/locales/zh-CN/common.json
git commit -m "✨ 设置面板增加终端高亮链接开关 #153"
```

---

### Task 7: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full frontend test suite**

Run: `npx vitest run`
Expected: PASS, including `terminalUrlScan`, `terminalUrlHighlighter`, `terminalThemeStore`, `terminalRegistry`.

- [ ] **Step 2: Lint the changed files**

Run: `npx eslint src/components/terminal/terminalUrlScan.ts src/components/terminal/terminalUrlHighlighter.ts src/components/terminal/terminalRegistry.ts src/components/terminal/Terminal.tsx src/components/settings/AppearanceSection.tsx src/stores/terminalThemeStore.ts`
Expected: no errors.

- [ ] **Step 3: Manual smoke (observe behavior, per Fix policy "verify by observing")**

Run the app, open a terminal, `echo "see https://example.com and http://a.test/path."`. Toggle Settings → Terminal → Highlight Links:
- ON: both URLs are tinted with the theme link color; trailing `.` is **not** tinted; scrolling up keeps older URLs tinted.
- OFF: URLs return to plain (underline only on hover); clicking still opens the browser in both states.
- Switch terminal theme: tint color updates to the new theme's link color.

Document anything that deviates; do not mark complete on assertion alone.

- [ ] **Step 4: Final commit (if any doc/cleanup pending)**

Nothing to commit if Tasks 1–6 are already committed. Otherwise commit remaining changes with a `#153` message.

---

## Self-Review notes

- **Spec coverage:** store toggle (T1) ✓; scan + column geometry + wrapped per-row (T2) ✓; overlay controller w/ diff reconcile, alt-buffer no-op, scrollback-on-scroll (T3) ✓; registry attach/dispose + shared `normalizeHttpUrl` + color helper, data handler unchanged (T4) ✓; Terminal.tsx enabled/color push, no recreation (T5) ✓; settings Switch + i18n en/zh (T6) ✓; tests + lint + observe (T7) ✓. Alt-buffer limitation documented in code comment + spec ✓.
- **Type consistency:** `attachTerminalUrlHighlighter(term, {enabled, color})` → `TerminalUrlHighlighterController { setEnabled, setColor, dispose }` used identically in T3/T4/T5. `findUrlRowSpans(buffer, start, end, cols) → TerminalUrlRowSpan{line,startCol,width,url}` consumed unchanged in T3. `terminalUrlHighlightColor(theme)` defined in T4, imported in T5.
- **No import cycle:** `terminalUrlScan` imports nothing from registry/highlighter; registry & highlighter import from scan. One-way.
