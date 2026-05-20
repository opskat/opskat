import { describe, it, expect, vi } from "vitest";
import type { Terminal as XTerminal } from "@xterm/xterm";
import {
  shouldRolloverWrite,
  attachXtermRolloverGuard,
  type XTermRolloverInternals,
} from "@/components/terminal/xtermRolloverGuard";

// shouldRolloverWrite 是纯判定: 给定一次 InputEvent 与 xterm 私有状态,
// 回答"我们是否要补一次 write"。所有补丁误判都应该在这里复现。
describe("shouldRolloverWrite", () => {
  function ev(opts: Partial<InputEvent>): InputEvent {
    return {
      inputType: "insertText",
      data: "x",
      isComposing: false,
      composed: true,
      ...opts,
    } as InputEvent;
  }

  const rollover: XTermRolloverInternals = { _keyDownSeen: true, _keyPressHandled: false };

  it("true: 真正的 rollover (IME 英文模式 / 高速连按), xterm 跳过且 keypress 未处理", () => {
    expect(shouldRolloverWrite(ev({ data: "a" }), rollover, false)).toBe(true);
  });

  // ---- 回归: 修复"按空格变两个空格" ----
  // 空格 keyCode=32 在 evaluateKeyboardEvent 不满足 keyCode>=48,
  // xterm 在 _keyDown 直接 return true(不 preventDefault) → keypress 路径
  // 调 triggerDataEvent 发送 + 置 _keyPressHandled=true。
  // 此时 textarea 仍然 insertText → input 事件触发,xterm 跳过(_keyPressHandled),
  // 旧补丁只看 _keyDownSeen 也补一次 → 双发。
  it("false: 空格 — keypress 已处理过, 不能再补", () => {
    expect(shouldRolloverWrite(ev({ data: " " }), { _keyDownSeen: true, _keyPressHandled: true }, false)).toBe(false);
  });

  // ---- 回归: 大写字母同源 ----
  // CoreBrowserTerminal._keyDown 的 macOS Caps Lock HACK (line 1072-1076)
  // 对所有 charCodeAt 65-90 的 key 都 return true → 走 keypress 路径,
  // 与空格同样被旧补丁误补。
  it("false: 大写字母 — Caps Lock / Shift HACK 已被 keypress 处理", () => {
    expect(shouldRolloverWrite(ev({ data: "A" }), { _keyDownSeen: true, _keyPressHandled: true }, false)).toBe(false);
  });

  // ---- xterm 自己处理的场景, 一律不补 ----
  it("false: isComposing — IME 拥有事件", () => {
    expect(shouldRolloverWrite(ev({ isComposing: true }), rollover, false)).toBe(false);
  });

  it("false: screenReaderMode 启用 — xterm 走另一条路径自己发", () => {
    expect(shouldRolloverWrite(ev({}), rollover, true)).toBe(false);
  });

  it("false: 非 insertText (paste / insertCompositionText)", () => {
    expect(shouldRolloverWrite(ev({ inputType: "insertFromPaste" }), rollover, false)).toBe(false);
    expect(shouldRolloverWrite(ev({ inputType: "insertCompositionText" }), rollover, false)).toBe(false);
  });

  it("false: composed=false — 合成事件或非用户输入", () => {
    expect(shouldRolloverWrite(ev({ composed: false }), rollover, false)).toBe(false);
  });

  it("false: 空 data", () => {
    expect(shouldRolloverWrite(ev({ data: null as unknown as string }), rollover, false)).toBe(false);
    expect(shouldRolloverWrite(ev({ data: "" }), rollover, false)).toBe(false);
  });

  it("false: _keyDownSeen=false — xterm 自己会处理 input 事件, 不需要补", () => {
    expect(shouldRolloverWrite(ev({}), { _keyDownSeen: false, _keyPressHandled: false }, false)).toBe(false);
  });

  // 防御 xterm 私有 API: _core 在小版本可能改名/消失,
  // 拿不到内部状态时应保守地不补(宁可漏 rollover,也不能双发)。
  it("false: internals=undefined — 私有 API 拿不到时保守跳过", () => {
    expect(shouldRolloverWrite(ev({}), undefined, false)).toBe(false);
  });
});

// 集成层: 真实 textarea + 真实 InputEvent + fake xterm-like wrapper,
// 验证 attach/detach 行为, 以及 write 回调时序。
describe("attachXtermRolloverGuard", () => {
  function makeFakeTerm(opts: {
    textarea: HTMLTextAreaElement | null;
    internals?: XTermRolloverInternals;
    screenReaderMode?: boolean;
  }): XTerminal {
    return {
      textarea: opts.textarea,
      options: { screenReaderMode: opts.screenReaderMode ?? false },
      _core: opts.internals,
    } as unknown as XTerminal;
  }

  function fireInput(ta: HTMLTextAreaElement, init: Partial<InputEvent>): void {
    // happy-dom 的 InputEvent 构造器支持 inputType/data,但不支持 isComposing(只读)。
    // 这里直接构造对象覆盖,绕过浏览器只读限制。
    const e = new Event("input", { bubbles: true, composed: true });
    Object.defineProperties(e, {
      inputType: { value: init.inputType ?? "insertText" },
      data: { value: init.data ?? "x" },
      isComposing: { value: init.isComposing ?? false },
      composed: { value: init.composed ?? true },
    });
    ta.dispatchEvent(e);
  }

  it("真正的 rollover 场景: 调用 write 一次", () => {
    const ta = document.createElement("textarea");
    const internals: XTermRolloverInternals = { _keyDownSeen: true, _keyPressHandled: false };
    const term = makeFakeTerm({ textarea: ta, internals });
    const write = vi.fn();

    attachXtermRolloverGuard(term, write);
    fireInput(ta, { data: "a" });

    expect(write).toHaveBeenCalledTimes(1);
    expect(write).toHaveBeenCalledWith("a");
  });

  it("空格的正常 keypress 路径: 不调用 write (核心回归用例)", () => {
    const ta = document.createElement("textarea");
    const internals: XTermRolloverInternals = { _keyDownSeen: true, _keyPressHandled: true };
    const term = makeFakeTerm({ textarea: ta, internals });
    const write = vi.fn();

    attachXtermRolloverGuard(term, write);
    fireInput(ta, { data: " " });

    expect(write).not.toHaveBeenCalled();
  });

  it("dispose 后 input 事件不再触发 write", () => {
    const ta = document.createElement("textarea");
    const internals: XTermRolloverInternals = { _keyDownSeen: true, _keyPressHandled: false };
    const term = makeFakeTerm({ textarea: ta, internals });
    const write = vi.fn();

    const guard = attachXtermRolloverGuard(term, write);
    guard.dispose();
    fireInput(ta, { data: "a" });

    expect(write).not.toHaveBeenCalled();
  });

  it("screenReaderMode 是动态读取的: 运行时切换立即生效", () => {
    const ta = document.createElement("textarea");
    const internals: XTermRolloverInternals = { _keyDownSeen: true, _keyPressHandled: false };
    const term = makeFakeTerm({ textarea: ta, internals, screenReaderMode: false });
    const write = vi.fn();

    attachXtermRolloverGuard(term, write);
    fireInput(ta, { data: "a" });
    expect(write).toHaveBeenCalledTimes(1);

    // 用户运行时打开 screenReaderMode
    (term.options as { screenReaderMode: boolean }).screenReaderMode = true;
    fireInput(ta, { data: "b" });
    expect(write).toHaveBeenCalledTimes(1);
  });

  it("textarea 为 null 时返回 no-op disposable, 不抛错", () => {
    const term = makeFakeTerm({ textarea: null });
    const write = vi.fn();
    const guard = attachXtermRolloverGuard(term, write);
    expect(() => guard.dispose()).not.toThrow();
    expect(write).not.toHaveBeenCalled();
  });
});
