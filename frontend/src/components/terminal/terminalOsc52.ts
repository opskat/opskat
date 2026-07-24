import type { Terminal as XTerminal } from "@xterm/xterm";

// OSC 52 是终端"选中缓冲区/剪贴板"操作序列：`ESC ] 52 ; Pc ; Pd ST`。
//   Pc = 目标缓冲区(c=clipboard、p=primary、s/0-7=cut buffer，可多字符)——我们只有一个
//        系统剪贴板，写哪个都归一到系统剪贴板。
//   Pd = base64 编码的内容；特例 Pd === "?" 是**读回请求**，要求终端把当前剪贴板回传给
//        程序。tmux `set-clipboard on`、vim `"+y`、以及远端 TUI 用它把内容送进本地剪贴板。
export type Osc52Action = { kind: "write"; text: string } | { kind: "read" } | { kind: "ignore" };

function decodeBase64Utf8(b64: string): string | null {
  try {
    const binary = atob(b64.trim());
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
  } catch {
    // atob 遇到非 base64 字符会抛 —— 恶意/损坏序列，忽略而不是让解析器崩掉。
    return null;
  }
}

// parseOsc52 只吃 OSC 标识符 `52;` 之后的部分，即 `Pc;Pd`（xterm registerOscHandler
// 回调拿到的 data）。纯函数，便于单测覆盖安全语义。
export function parseOsc52(payload: string): Osc52Action {
  const sep = payload.indexOf(";");
  if (sep === -1) return { kind: "ignore" };
  const data = payload.slice(sep + 1);
  if (data === "?") return { kind: "read" };
  const text = decodeBase64Utf8(data);
  if (text === null || text === "") return { kind: "ignore" };
  return { kind: "write", text };
}

export interface TerminalClipboardOsc52Controller {
  setEnabled(enabled: boolean): void;
  dispose(): void;
}

// 在终端解析器上挂一个 OSC 52 处理器：启用时把远端写入的内容解码后送进系统剪贴板。
// 安全约束：**永不响应读回请求**（parseOsc52 的 "read" 分支被丢弃），否则远端会话能
// 反向读走本地剪贴板。启用状态由调用方通过 enabled/setEnabled 联动设置项。
export function attachTerminalClipboardOsc52(
  term: XTerminal,
  opts: { enabled: boolean; write: (text: string) => void }
): TerminalClipboardOsc52Controller {
  let enabled = opts.enabled;
  const disposable = term.parser.registerOscHandler(52, (payload) => {
    // 关闭时返回 false = 未处理，xterm 直接丢弃该序列（无默认处理器），等价 no-op。
    if (!enabled) return false;
    const action = parseOsc52(payload);
    if (action.kind === "write") opts.write(action.text);
    // read / ignore：吞掉序列但不做任何事（尤其绝不回写剪贴板给远端）。
    return true;
  });
  return {
    setEnabled: (next) => {
      enabled = next;
    },
    dispose: () => disposable.dispose(),
  };
}
