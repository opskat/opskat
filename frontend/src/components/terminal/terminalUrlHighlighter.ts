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
  let rafPending = false;
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
    // Known limitation (by design, not a bug): no highlighting in the alternate
    // buffer. registerDecoration returns undefined there and registerMarker only
    // attaches to the normal buffer, so full-screen TUIs (less/vim/git pager/htop)
    // can't be tinted. Clicking via WebLinksAddon is unaffected.
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
    if (disposed || rafPending) return;
    rafPending = true;
    rafId = requestAnimationFrame(() => {
      rafPending = false;
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
      if (rafPending && rafId) cancelAnimationFrame(rafId);
      for (const s of subs) s.dispose();
      clearAll();
    },
  };
}
