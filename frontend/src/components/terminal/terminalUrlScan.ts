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

    // Always advance, even if getLine(y) returned undefined (yy === y): getLine
    // can return undefined for an in-range row if the buffer is trimmed
    // concurrently, and y = yy alone would spin the outer loop forever.
    y = Math.max(yy, y + 1); // skip past the wrapped continuation rows we already consumed
  }
  return spans;
}
