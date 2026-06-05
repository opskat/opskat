import type { Terminal } from "@xterm/xterm";

type Disposable = { dispose: () => void };
type Marker = Disposable;
type Decoration = Disposable;
type LinkDecoration = { marker: Marker; decoration: Decoration };

interface BufferLine {
  translateToString(trimRight?: boolean): string;
}

interface TerminalLink {
  range: {
    start: { x: number; y: number };
    end: { x: number; y: number };
  };
  text: string;
  activate: (event: MouseEvent | undefined, text: string) => void;
}

interface LinkProvider {
  provideLinks: (bufferLineNumber: number, callback: (links: TerminalLink[] | undefined) => void) => void;
}

interface TerminalUrlLinkHost {
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
  registerLinkProvider: (provider: LinkProvider) => Disposable;
  registerMarker: (cursorYOffset?: number) => Marker | undefined;
  registerDecoration: (options: {
    marker: Marker;
    x?: number;
    width?: number;
    foregroundColor?: string;
  }) => Decoration | undefined;
  onWriteParsed: (listener: () => void) => Disposable;
  onScroll: (listener: () => void) => Disposable;
  onResize: (listener: () => void) => Disposable;
}

const HTTP_URL_PATTERN = /https?:\/\/[^\s<>"'`]+/gi;
const TRAILING_URL_PUNCTUATION = /[),.;!?\]}]+$/;
const HEX_COLOR_PATTERN = /^#[0-9a-f]{6}$/i;

export interface TerminalUrlLinksController extends Disposable {
  colorizeOutput: (text: string) => string;
  setForegroundColor: (color: string | undefined) => void;
}

export function attachTerminalUrlLinks(
  term: Terminal,
  openURL: (url: string) => void,
  foregroundColor: string | undefined
): TerminalUrlLinksController {
  const host = term as unknown as TerminalUrlLinkHost;
  const providerDispose = host.registerLinkProvider({
    provideLinks: (bufferLineNumber, callback) => {
      const line = host.buffer.active.getLine(bufferLineNumber - 1);
      if (!line) {
        callback(undefined);
        return;
      }

      const links = findHttpUrls(line.translateToString(true)).map((match) => ({
        range: {
          start: { x: match.start + 1, y: bufferLineNumber },
          end: { x: match.end, y: bufferLineNumber },
        },
        text: match.url,
        activate: (_event: MouseEvent | undefined, text: string) => {
          const url = normalizeHttpUrl(text);
          if (url) openURL(url);
        },
      }));
      callback(links.length > 0 ? links : undefined);
    },
  });

  let linkColor = validDecorationColor(foregroundColor);
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
    if (!linkColor) return;

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
        });
        if (decoration) decorations.push({ marker, decoration });
      }
    }
  };

  const writeDispose = host.onWriteParsed(refreshDecorations);
  const scrollDispose = host.onScroll(refreshDecorations);
  const resizeDispose = host.onResize(refreshDecorations);
  refreshDecorations();

  return {
    colorizeOutput: (text) => colorizeHttpUrls(text, linkColor),
    setForegroundColor: (color) => {
      linkColor = validDecorationColor(color);
      refreshDecorations();
    },
    dispose: () => {
      clearDecorations();
      resizeDispose.dispose();
      scrollDispose.dispose();
      writeDispose.dispose();
      providerDispose.dispose();
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

function colorizeHttpUrls(text: string, color: string | undefined): string {
  const rgb = color ? hexToRgb(color) : undefined;
  if (!rgb) return text;
  return text.replace(HTTP_URL_PATTERN, (rawUrl: string) => {
    const url = trimTrailingUrlPunctuation(rawUrl);
    if (!normalizeHttpUrl(url)) return rawUrl;
    const suffix = rawUrl.slice(url.length);
    return `\x1b[38;2;${rgb.r};${rgb.g};${rgb.b}m${url}\x1b[39m${suffix}`;
  });
}

function hexToRgb(color: string): { r: number; g: number; b: number } | undefined {
  if (!HEX_COLOR_PATTERN.test(color)) return undefined;
  return {
    r: Number.parseInt(color.slice(1, 3), 16),
    g: Number.parseInt(color.slice(3, 5), 16),
    b: Number.parseInt(color.slice(5, 7), 16),
  };
}