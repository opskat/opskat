import type { Terminal } from "@xterm/xterm";

type Disposable = { dispose: () => void };
type Marker = Disposable;
type Decoration = Disposable;
type LinkDecoration = { marker: Marker; decoration: Decoration };

interface BufferLine {
  translateToString(trimRight?: boolean): string;
}

interface TerminalUrlHighlightHost {
  rows: number;
  buffer: {
    active: {
      baseY: number;
      cursorY: number;
      viewportY: number;
      length: number;
      getLine: (y: number) => BufferLine | undefined;
    };
  };
  registerMarker: (cursorYOffset?: number) => Marker | undefined;
  registerDecoration: (options: {
    marker: Marker;
    x?: number;
    width?: number;
    foregroundColor?: string;
    layer?: "bottom" | "top";
  }) => Decoration | undefined;
  onWriteParsed: (listener: () => void) => Disposable;
  onScroll: (listener: () => void) => Disposable;
  onResize: (listener: () => void) => Disposable;
  refresh: (start: number, end: number) => void;
}

const HTTP_URL_PATTERN = /https?:\/\/[^\s<>"'`]+/gi;
const TRAILING_URL_PUNCTUATION = /[),.;!?\]}]+$/;
const HEX_COLOR_PATTERN = /^#[0-9a-f]{6}$/i;

export interface TerminalUrlHighlighterController extends Disposable {
  setEnabled: (enabled: boolean) => void;
  setForegroundColor: (color: string | undefined) => void;
}

export function attachTerminalUrlHighlighter(
  term: Terminal,
  enabled: boolean,
  foregroundColor: string | undefined
): TerminalUrlHighlighterController {
  const host = term as unknown as TerminalUrlHighlightHost;
  let linkColor = validDecorationColor(foregroundColor);
  let highlightEnabled = enabled;
  let decorations: LinkDecoration[] = [];

  const clearDecorations = () => {
    for (const { marker, decoration } of decorations) {
      decoration.dispose();
      marker.dispose();
    }
    decorations = [];
  };

  const refreshDecorations = () => {
    clearDecorations();
    if (!highlightEnabled || !linkColor) {
      host.refresh(0, Math.max(0, host.rows - 1));
      return;
    }

    const buffer = host.buffer.active;
    const firstLine = buffer.viewportY;
    const lastLine = Math.min(buffer.length - 1, firstLine + Math.max(1, host.rows));
    for (let lineNumber = firstLine; lineNumber <= lastLine; lineNumber += 1) {
      const line = buffer.getLine(lineNumber);
      if (!line) continue;
      for (const match of findHttpUrls(line.translateToString(true))) {
        const marker = host.registerMarker(lineNumber - (buffer.baseY + buffer.cursorY));
        if (!marker) continue;
        const decoration = host.registerDecoration({
          marker,
          x: match.start,
          width: match.end - match.start,
          foregroundColor: linkColor,
          layer: "top",
        });
        if (decoration) decorations.push({ marker, decoration });
      }
    }
    host.refresh(0, Math.max(0, host.rows - 1));
  };

  const writeDispose = host.onWriteParsed(refreshDecorations);
  const scrollDispose = host.onScroll(refreshDecorations);
  const resizeDispose = host.onResize(refreshDecorations);
  refreshDecorations();

  return {
    setEnabled: (nextEnabled) => {
      highlightEnabled = nextEnabled;
      refreshDecorations();
    },
    setForegroundColor: (color) => {
      linkColor = validDecorationColor(color);
      refreshDecorations();
    },
    dispose: () => {
      clearDecorations();
      resizeDispose.dispose();
      scrollDispose.dispose();
      writeDispose.dispose();
    },
  };
}

function findHttpUrls(line: string): Array<{ url: string; start: number; end: number }> {
  const matches: Array<{ url: string; start: number; end: number }> = [];
  for (const match of line.matchAll(HTTP_URL_PATTERN)) {
    const rawUrl = match[0];
    const url = trimTrailingUrlPunctuation(rawUrl);
    if (!normalizeHttpUrl(url)) continue;
    const start = match.index ?? 0;
    matches.push({ url, start, end: start + url.length });
  }
  return matches;
}

function trimTrailingUrlPunctuation(url: string): string {
  return url.replace(TRAILING_URL_PUNCTUATION, "");
}

function normalizeHttpUrl(url: string): string | undefined {
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
    return url;
  } catch {
    return undefined;
  }
}

function validDecorationColor(color: string | undefined): string | undefined {
  if (!color) return undefined;
  return HEX_COLOR_PATTERN.test(color) ? color : undefined;
}
