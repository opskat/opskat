import { describe, expect, it } from "vitest";
import { Terminal } from "@xterm/xterm";
import { buildK8sLogDownloadFilename, k8sLogXtermOptions, prependXtermLogText } from "./k8sLogTerminalText";

describe("k8sLogXtermOptions", () => {
  it("enables proposed APIs so search decorations can highlight matches", () => {
    expect(k8sLogXtermOptions().allowProposedApi).toBe(true);
  });
});

describe("buildK8sLogDownloadFilename", () => {
  it("builds a safe log file name", () => {
    expect(buildK8sLogDownloadFilename("default", "api-server", "app")).toBe("default-api-server-app.log");
    expect(buildK8sLogDownloadFilename("kube-system", "coredns/abc", "")).toBe("kube-system-coredns_abc.log");
  });
});

function writeToTerminal(term: Terminal, text: string): Promise<void> {
  return new Promise((resolve) => {
    term.write(text, () => resolve());
  });
}

function topVisibleLine(term: Terminal): string {
  const buffer = term.buffer.active;
  return buffer.getLine(buffer.viewportY)?.translateToString(true) ?? "";
}

describe("prependXtermLogText", () => {
  it("keeps the visible lines in place when older logs are prepended at the top", async () => {
    const term = new Terminal({ rows: 10, cols: 40, scrollback: 25000, convertEol: true });
    await writeToTerminal(term, Array.from({ length: 500 }, (_, i) => `line-${i}`).join("\n") + "\n");
    term.scrollToLine(0);
    expect(topVisibleLine(term)).toBe("line-0");

    await prependXtermLogText(term, "older-0\nolder-1\nolder-2\n");

    // 更早的日志插在视口上方：视口仍停在 line-0，但已离开顶部（viewportY > 0），
    // 继续上滑会产生 scroll 事件，下一次加载才不靠“先下滑再上滑”。
    expect(topVisibleLine(term)).toBe("line-0");
    expect(term.buffer.active.viewportY).toBe(3);
    term.dispose();
  });
});
