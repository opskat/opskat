import { describe, it, expect } from "vitest";
import { Terminal } from "@xterm/xterm";
import { parseOsc52, attachTerminalClipboardOsc52 } from "@/components/terminal/terminalOsc52";

// base64 of a UTF-8 string, as a terminal/tmux emits it in an OSC 52 payload.
function b64(text: string): string {
  return Buffer.from(text, "utf8").toString("base64");
}

describe("parseOsc52", () => {
  it("decodes a clipboard write payload to UTF-8 text", () => {
    expect(parseOsc52(`c;${b64("hello world")}`)).toEqual({ kind: "write", text: "hello world" });
  });

  it("decodes multibyte UTF-8 correctly", () => {
    expect(parseOsc52(`c;${b64("你好，clipboard")}`)).toEqual({ kind: "write", text: "你好，clipboard" });
  });

  it("treats any selection target the same (c / p / multiple)", () => {
    expect(parseOsc52(`p;${b64("primary")}`)).toEqual({ kind: "write", text: "primary" });
    expect(parseOsc52(`cp;${b64("both")}`)).toEqual({ kind: "write", text: "both" });
    // An empty Pc defaults to the clipboard per the OSC 52 spec.
    expect(parseOsc52(`;${b64("default-target")}`)).toEqual({ kind: "write", text: "default-target" });
  });

  it("flags a read request (Pd === '?') so the caller can refuse it", () => {
    // SECURITY: honoring this would leak the local clipboard to a remote session.
    expect(parseOsc52("c;?")).toEqual({ kind: "read" });
    expect(parseOsc52("p;?")).toEqual({ kind: "read" });
  });

  it("ignores malformed payloads instead of throwing", () => {
    expect(parseOsc52("")).toEqual({ kind: "ignore" });
    expect(parseOsc52("c")).toEqual({ kind: "ignore" }); // no separator
    expect(parseOsc52("c;!!!not-base64!!!")).toEqual({ kind: "ignore" });
  });
});

describe("attachTerminalClipboardOsc52 against real @xterm/xterm", () => {
  it("writes decoded text to the clipboard when a full OSC 52 sequence is parsed", async () => {
    const term = new Terminal({ cols: 80, rows: 24 });
    const writes: string[] = [];
    const ctrl = attachTerminalClipboardOsc52(term, { enabled: true, write: (t) => writes.push(t) });
    await new Promise<void>((resolve) => term.write(`\x1b]52;c;${b64("clip-me")}\x07`, () => resolve()));
    expect(writes).toEqual(["clip-me"]);
    ctrl.dispose();
    term.dispose();
  });

  it("does NOT touch the clipboard when disabled", async () => {
    const term = new Terminal({ cols: 80, rows: 24 });
    const writes: string[] = [];
    const ctrl = attachTerminalClipboardOsc52(term, { enabled: false, write: (t) => writes.push(t) });
    await new Promise<void>((resolve) => term.write(`\x1b]52;c;${b64("blocked")}\x07`, () => resolve()));
    expect(writes).toEqual([]);
    ctrl.dispose();
    term.dispose();
  });

  it("never writes for a read-back request (clipboard is not leaked)", async () => {
    const term = new Terminal({ cols: 80, rows: 24 });
    const writes: string[] = [];
    const ctrl = attachTerminalClipboardOsc52(term, { enabled: true, write: (t) => writes.push(t) });
    await new Promise<void>((resolve) => term.write(`\x1b]52;c;?\x07`, () => resolve()));
    expect(writes).toEqual([]);
    ctrl.dispose();
    term.dispose();
  });

  it("honors setEnabled toggled at runtime", async () => {
    const term = new Terminal({ cols: 80, rows: 24 });
    const writes: string[] = [];
    const ctrl = attachTerminalClipboardOsc52(term, { enabled: false, write: (t) => writes.push(t) });
    ctrl.setEnabled(true);
    await new Promise<void>((resolve) => term.write(`\x1b]52;c;${b64("now-on")}\x07`, () => resolve()));
    expect(writes).toEqual(["now-on"]);
    ctrl.dispose();
    term.dispose();
  });
});
