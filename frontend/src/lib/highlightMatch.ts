export interface HighlightSegment {
  text: string;
  match: boolean;
}

export function highlightMatch(label: string, query: string): HighlightSegment[] {
  if (!query) return [{ text: label, match: false }];
  const idx = label.toLowerCase().indexOf(query.toLowerCase());
  if (idx === -1) return [{ text: label, match: false }];
  const segments: HighlightSegment[] = [];
  if (idx > 0) segments.push({ text: label.slice(0, idx), match: false });
  segments.push({ text: label.slice(idx, idx + query.length), match: true });
  if (idx + query.length < label.length) {
    segments.push({ text: label.slice(idx + query.length), match: false });
  }
  return segments;
}

export function filterMatches(label: string, query: string): boolean {
  if (!query) return true;
  return label.toLowerCase().includes(query.toLowerCase());
}
