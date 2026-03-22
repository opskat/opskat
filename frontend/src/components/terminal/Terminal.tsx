import { useEffect, useRef } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { WriteSSH, ResizeSSH } from "../../../wailsjs/go/main/App";
import { EventsOn, EventsOff } from "../../../wailsjs/runtime/runtime";

interface TerminalProps {
  sessionId: string;
  active: boolean;
}

export function Terminal({ sessionId, active }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    const term = new XTerminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
      theme: {
        background: "var(--terminal-bg, #1a1b26)",
        foreground: "var(--terminal-fg, #a9b1d6)",
        cursor: "var(--terminal-cursor, #c0caf5)",
      },
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(containerRef.current);

    // 初始 fit
    requestAnimationFrame(() => {
      fitAddon.fit();
    });

    termRef.current = term;
    fitAddonRef.current = fitAddon;

    // 用户输入 → 后端
    const onDataDispose = term.onData((data) => {
      const encoded = btoa(
        String.fromCharCode(...new TextEncoder().encode(data))
      );
      WriteSSH(sessionId, encoded).catch(console.error);
    });

    // 后端输出 → 终端
    const eventName = "ssh:data:" + sessionId;
    EventsOn(eventName, (dataB64: string) => {
      const binary = atob(dataB64);
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
      }
      term.write(bytes);
    });

    // 会话关闭事件
    const closedEvent = "ssh:closed:" + sessionId;
    EventsOn(closedEvent, () => {
      term.write("\r\n\x1b[31m[Connection closed]\x1b[0m\r\n");
    });

    // 窗口尺寸变化
    const resizeObserver = new ResizeObserver(() => {
      requestAnimationFrame(() => {
        fitAddon.fit();
        const dims = fitAddon.proposeDimensions();
        if (dims) {
          ResizeSSH(sessionId, dims.cols, dims.rows).catch(console.error);
        }
      });
    });
    resizeObserver.observe(containerRef.current);

    return () => {
      onDataDispose.dispose();
      EventsOff(eventName);
      EventsOff(closedEvent);
      resizeObserver.disconnect();
      term.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [sessionId]);

  // 当 tab 切换回来时重新 fit
  useEffect(() => {
    if (active && fitAddonRef.current) {
      requestAnimationFrame(() => {
        fitAddonRef.current?.fit();
      });
    }
  }, [active]);

  return (
    <div
      ref={containerRef}
      className="h-full w-full"
      style={{ padding: "4px" }}
    />
  );
}
