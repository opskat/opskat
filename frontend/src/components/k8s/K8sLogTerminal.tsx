import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";
import type { SearchAddon } from "@xterm/addon-search";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon as XtermSearchAddon } from "@xterm/addon-search";
import "@xterm/xterm/css/xterm.css";
import { useTerminalThemeStore, toXtermTheme } from "@/stores/terminalThemeStore";
import { builtinThemes, defaultLightTheme, defaultDarkTheme } from "@/data/terminalThemes";
import { useResolvedTheme } from "@/components/theme-provider";
import { TerminalSearchBar } from "@/components/terminal/TerminalSearchBar";
import { getXtermBufferText, k8sLogXtermOptions, prependXtermLogText } from "./k8sLogTerminalText";

export interface K8sLogTerminalHandle {
  write: (data: string | Uint8Array) => void;
  prepend: (data: string) => Promise<void>;
  clear: () => void;
  getLogText: () => string;
  toggleSearch: () => void;
  openSearch: (query?: string | null) => void;
}

export function K8sLogTerminal({ ref, onReachTop }: { ref?: Ref<K8sLogTerminalHandle>; onReachTop?: () => void }) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [searchAddon, setSearchAddon] = useState<SearchAddon | null>(null);
  const [showSearch, setShowSearch] = useState(false);
  const [searchRequest, setSearchRequest] = useState<{ query: string | null; token: number }>({
    query: null,
    token: 0,
  });

  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const selectedThemeId = useTerminalThemeStore((s) => s.selectedThemeId);
  const customThemes = useTerminalThemeStore((s) => s.customThemes);
  const resolvedTheme = useResolvedTheme();

  const xtermTheme = useMemo(() => {
    if (selectedThemeId === "default") {
      return resolvedTheme === "light" ? toXtermTheme(defaultLightTheme) : toXtermTheme(defaultDarkTheme);
    }
    const theme =
      builtinThemes.find((t) => t.id === selectedThemeId) || customThemes.find((t) => t.id === selectedThemeId);
    return theme ? toXtermTheme(theme) : undefined;
  }, [selectedThemeId, customThemes, resolvedTheme]);

  const onReachTopRef = useRef(onReachTop);
  // eslint-disable-next-line react-hooks/refs
  onReachTopRef.current = onReachTop;

  const openSearch = useCallback((query: string | null = null) => {
    setSearchRequest((req) => ({ query, token: req.token + 1 }));
    setShowSearch(true);
  }, []);

  useImperativeHandle(ref, () => ({
    write: (data: string | Uint8Array) => {
      termRef.current?.write(data);
    },
    prepend: (data: string) => {
      const term = termRef.current;
      if (!term) return Promise.resolve();
      return prependXtermLogText(term, data);
    },
    clear: () => {
      termRef.current?.clear();
    },
    getLogText: () => {
      const term = termRef.current;
      return term ? getXtermBufferText(term) : "";
    },
    toggleSearch: () => {
      setSearchRequest((req) => ({ query: null, token: req.token + 1 }));
      setShowSearch((visible) => !visible);
    },
    openSearch,
  }));

  useEffect(() => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return;

    const term = new XTerminal(k8sLogXtermOptions());

    const fitAddon = new FitAddon();
    const search = new XtermSearchAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(search);
    term.open(wrapper);

    fitAddon.fit();
    termRef.current = term;
    fitAddonRef.current = fitAddon;
    setSearchAddon(search);

    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit();
    });
    resizeObserver.observe(wrapper);

    let reachTopPending = false;
    const onScroll = () => {
      if (!onReachTopRef.current) return;
      if (term.buffer.active.viewportY > 0) {
        reachTopPending = false;
        return;
      }
      if (reachTopPending) return;
      reachTopPending = true;
      onReachTopRef.current();
      window.setTimeout(() => {
        reachTopPending = false;
      }, 400);
    };
    const scrollDisposable = term.onScroll(onScroll);

    return () => {
      scrollDisposable.dispose();
      resizeObserver.disconnect();
      setSearchAddon(null);
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

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "f") return;
      event.preventDefault();
      openSearch();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [openSearch]);

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <TerminalSearchBar
        visible={showSearch}
        onClose={() => setShowSearch(false)}
        searchAddon={searchAddon}
        initialQuery={searchRequest.query}
        initialQueryToken={searchRequest.token}
      />
      <div ref={wrapperRef} className="flex-1 w-full rounded-lg overflow-hidden min-h-0" />
    </div>
  );
}
