import { useEffect, useImperativeHandle, useRef, type Ref } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { useTerminalThemeStore, useXtermTheme } from "@/stores/terminalThemeStore";

export interface K8sLogTerminalHandle {
  write: (data: string | Uint8Array) => void;
  clear: () => void;
}

export function K8sLogTerminal({ ref }: { ref?: Ref<K8sLogTerminalHandle> }) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);

  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const xtermTheme = useXtermTheme();

  useImperativeHandle(ref, () => ({
    write: (data: string | Uint8Array) => {
      termRef.current?.write(data);
    },
    clear: () => {
      termRef.current?.clear();
    },
  }));

  useEffect(() => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return;

    const term = new XTerminal({
      cursorBlink: false,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
      disableStdin: true,
      convertEol: true,
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(wrapper);

    fitAddon.fit();
    termRef.current = term;
    fitAddonRef.current = fitAddon;

    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit();
    });
    resizeObserver.observe(wrapper);

    return () => {
      resizeObserver.disconnect();
      term.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, []);

  useEffect(() => {
    if (!termRef.current) return;
    termRef.current.options.theme = xtermTheme;
    termRef.current.options.fontSize = fontSize;
    termRef.current.options.scrollback = scrollback;
    fitAddonRef.current?.fit();
  }, [xtermTheme, fontSize, scrollback]);

  return <div ref={wrapperRef} className="flex-1 w-full rounded-lg overflow-hidden min-h-0" />;
}
