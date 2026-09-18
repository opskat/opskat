import type { ITerminalOptions, Terminal } from "@xterm/xterm";

/** SearchAddon 高亮走 registerDecoration，必须打开 proposed API，否则 findNext/findPrevious 会抛错。 */
export function k8sLogXtermOptions(): ITerminalOptions {
  return {
    cursorBlink: false,
    fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
    disableStdin: true,
    convertEol: true,
    allowProposedApi: true,
  };
}

/** 导出 xterm 当前缓冲区（含 scrollback）为纯文本。 */
export function getXtermBufferText(term: Terminal): string {
  const buffer = term.buffer.active;
  const lines: string[] = [];
  for (let i = 0; i < buffer.length; i++) {
    const line = buffer.getLine(i);
    if (!line) continue;
    lines.push(line.translateToString(true));
  }
  return lines.join("\n");
}

export function buildK8sLogDownloadFilename(namespace: string, podName: string, container: string) {
  const safe = (value: string) => value.replace(/[^\w.-]+/g, "_");
  const containerPart = container ? `-${safe(container)}` : "";
  return `${safe(namespace)}-${safe(podName)}${containerPart}.log`;
}
