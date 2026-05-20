// 上游 bug 旁路 (xterm v6.0.0, CoreBrowserTerminal._inputEvent):
// xterm 用全局 _keyDownSeen 给 IME composed insertText 做去重,
// 假定一次只按一个键。百度五笔等输入法在「英文模式」下把每个按键都
// 伪装成 keyCode=229, 加上用户快速输入造成 key-rollover (前一键
// keyup 之前下一键 input 已触发), xterm 误判「_keyDownSeen=true =>
// 重复输入」把中间字符丢弃。
//
// 旁路逻辑: 监听 textarea 的 input 事件, 在 xterm 跳过且 keypress
// 路径没处理过的场景下补一次 write。条件必须严于 xterm 的跳过条件,
// 否则会把"xterm 通过 keypress 路径正常发送的字符"(空格 keyCode=32,
// 大写字母走 Caps Lock HACK) 当成漏字符再补一次,造成双发。
//
// 参考: node_modules/.pnpm/@xterm+xterm@6.0.0/.../CoreBrowserTerminal.ts
//   - _keyDown 设置 _keyDownSeen=true (line 1023)
//   - _keyPress 处理完置 _keyPressHandled=true (line 1177)
//   - _inputEvent 跳过条件 (line 1196): (!composed || !_keyDownSeen) ||
//     _keyPressHandled || screenReaderMode

import type { Terminal as XTerminal } from "@xterm/xterm";

export interface XTermRolloverInternals {
  _keyDownSeen?: boolean;
  _keyPressHandled?: boolean;
}

export interface XTermRolloverGuard {
  dispose(): void;
}

export function shouldRolloverWrite(
  ev: InputEvent,
  internals: XTermRolloverInternals | undefined,
  screenReaderMode: boolean | undefined
): boolean {
  if (screenReaderMode) return false;
  if (ev.inputType !== "insertText") return false;
  if (!ev.data) return false;
  if (ev.isComposing) return false;
  if (!ev.composed) return false;
  // 拿不到 xterm 内部状态时保守跳过: 宁可漏一个 rollover, 也不能双发。
  if (!internals) return false;
  // xterm 跳过 input 的条件之一: composed && _keyDownSeen。
  // 这是 rollover bug 的必要前提 —— xterm 没有自己消化掉这次 input。
  if (internals._keyDownSeen !== true) return false;
  // 但若 xterm 的 keypress 路径已经发过 (空格 / 大写字母),
  // 我们再补就成了双发 —— 这是 v1.6.0 之前的 bug 根因。
  if (internals._keyPressHandled === true) return false;
  return true;
}

export function attachXtermRolloverGuard(term: XTerminal, write: (data: string) => void): XTermRolloverGuard {
  const ta = term.textarea;
  if (!ta) {
    return { dispose: () => {} };
  }
  const internals = (term as unknown as { _core?: XTermRolloverInternals })._core;
  const handler = (e: Event) => {
    const ie = e as InputEvent;
    if (shouldRolloverWrite(ie, internals, term.options.screenReaderMode)) {
      write(ie.data!);
    }
  };
  ta.addEventListener("input", handler, true);
  return {
    dispose: () => ta.removeEventListener("input", handler, true),
  };
}
