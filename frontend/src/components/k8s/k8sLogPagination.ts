export const K8S_LOG_INITIAL_TAIL_LINES = 200;
export const K8S_LOG_LOAD_MORE_LINES = 200;
export const K8S_LOG_MAX_TAIL_LINES = 10000;

export function splitLogLines(text: string): string[] {
  if (!text) return [];
  const lines = text.split("\n");
  if (text.endsWith("\n") && lines.length > 0 && lines[lines.length - 1] === "") {
    lines.pop();
  }
  return lines;
}

/** 从「末尾 requestedTailLines 行」的快照中，取出比 previousTailLines 窗口更早的日志文本。 */
export function extractOlderLogLines(
  snapshot: string,
  previousTailLines: number,
  requestedTailLines: number,
): { older: string; reachedStart: boolean } {
  const lines = splitLogLines(snapshot);
  const reachedStart = lines.length < requestedTailLines;
  if (lines.length <= previousTailLines) {
    return { older: "", reachedStart };
  }
  const olderLines = lines.slice(0, lines.length - previousTailLines);
  const older = olderLines.length ? `${olderLines.join("\n")}\n` : "";
  return { older, reachedStart };
}
