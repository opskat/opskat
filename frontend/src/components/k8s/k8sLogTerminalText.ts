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
export function countLogTextLines(text: string): number {
  if (!text) return 0;
  const lines = text.split("\n");
  if (text.endsWith("\n") && lines.length > 0 && lines[lines.length - 1] === "") {
    return lines.length - 1;
  }
  return lines.length;
}

/**
 * 在现有缓冲区前插入更早的日志，并让视口停在原来的内容上。
 *
 * 返回的 Promise 在内容真正进入缓冲区后才 resolve：xterm 的 write 是异步解析的，
 * 同步读 buffer 只会读到 clear() 后的空缓冲区，视口就会停在顶部不动，
 * 顶部不再产生 scroll 事件，用户必须下滑再上滑才能触发下一次加载。
 *
 * @returns 写入已落到缓冲区（Promise 可在组件里 await，把视口校正算作加载的一部分）。
 */
export function prependXtermLogText(term: Terminal, prefix: string): Promise<void> {
  if (!prefix) return Promise.resolve();
  const active = term.buffer.active;
  const offsetFromBottom = active.length - 1 - active.viewportY;
  const existing = getXtermBufferText(term);
  term.clear();
  return new Promise((resolve) => {
    term.write(prefix + existing, () => {
      const nextActive = term.buffer.active;
      const targetLine = Math.max(0, nextActive.length - 1 - offsetFromBottom);
      term.scrollToLine(targetLine);
      resolve();
    });
  });
}

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
