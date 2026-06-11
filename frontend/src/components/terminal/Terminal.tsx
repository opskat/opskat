import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  forwardRef,
  type CSSProperties,
  type MouseEvent as ReactMouseEvent,
} from "react";
import type { Terminal as XTerminal } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";
import type { SearchAddon } from "@xterm/addon-search";
import { ClipboardSetText } from "../../../wailsjs/runtime/runtime";
import { useShortcutStore, formatBinding } from "@/stores/shortcutStore";
import { useTerminalStore, TRANSPORTS } from "@/stores/terminalStore";
import { useTerminalThemeStore, toXtermTheme } from "@/stores/terminalThemeStore";
import { builtinThemes, defaultLightTheme, defaultDarkTheme } from "@/data/terminalThemes";
import { withTerminalFontFallback, withTerminalFontIsolation } from "@/data/terminalFonts";
import { useResolvedTheme } from "@/components/theme-provider";
import { useTranslation } from "react-i18next";
import { notifyCopied } from "@/lib/notify";
import {
  Button,
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
} from "@opskat/ui";
import { Copy, Search } from "lucide-react";
import { TerminalSearchBar } from "./TerminalSearchBar";
import { getTerminalContextMenuAction } from "./terminalContextMenuPolicy";
import { useSFTPStore } from "@/stores/sftpStore";
import { useTabStore } from "@/stores/tabStore";
import {
  getOrCreateTerminal,
  getTerminalInstance,
  pasteFromClipboard,
  terminalUrlHighlightColor,
  uploadFilesWithRz,
} from "./terminalRegistry";
import { registerTerminalFileDropTarget } from "./terminalFileDropCoordinator";

export interface TerminalHandle {
  toggleSearch: () => void;
}

interface TerminalProps {
  sessionId: string;
  active: boolean;
  tabId: string;
}

export const Terminal = forwardRef<TerminalHandle, TerminalProps>(function Terminal({ sessionId, active, tabId }, ref) {
  const { t } = useTranslation();
  const wrapperRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const searchAddonRef = useRef<SearchAddon | null>(null);
  const [showSearch, setShowSearch] = useState(false);
  const [searchRequest, setSearchRequest] = useState<{ query: string | null; token: number }>({
    query: null,
    token: 0,
  });
  const [hasSelection, setHasSelection] = useState(false);
  const [floatingCopyPosition, setFloatingCopyPosition] = useState<{ left: number; top: number } | null>(null);
  const [isDragOver, setIsDragOver] = useState(false);
  const lastAutoCopiedSelectionRef = useRef("");
  const shortcuts = useShortcutStore((s) => s.shortcuts);
  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const fontFamily = useTerminalThemeStore((s) => s.fontFamily);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const webglEnabled = useTerminalThemeStore((s) => s.webglEnabled);
  const highlightLinks = useTerminalThemeStore((s) => s.highlightLinks);
  const copyBehavior = useTerminalThemeStore((s) => s.copyBehavior);
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
  const transport = useTerminalStore((s) => s.tabData[tabId]?.panes[sessionId]?.transport ?? "ssh");
  const spec = TRANSPORTS[transport];
  const paneConnected = useTerminalStore((s) => s.tabData[tabId]?.panes[sessionId]?.connected ?? false);
  const terminalDropEnabled = active && paneConnected && transport === "ssh";

  useImperativeHandle(ref, () => ({
    toggleSearch: () => {
      setSearchRequest((req) => ({ query: null, token: req.token + 1 }));
      setShowSearch((v) => !v);
    },
  }));

  const copyText = useCallback(
    (selection: string) => {
      ClipboardSetText(selection)
        .then(() => notifyCopied(t("ssh.contextMenu.copied")))
        .catch(console.error);
      lastAutoCopiedSelectionRef.current = selection;
      return true;
    },
    [t]
  );

  const copySelection = useCallback(() => {
    const selection = termRef.current?.getSelection();
    if (!selection) return false;
    return copyText(selection);
  }, [copyText]);

  const handleCopy = useCallback(() => {
    copySelection();
  }, [copySelection]);

  const openSearch = useCallback((query: string | null = null) => {
    setSearchRequest((req) => ({ query, token: req.token + 1 }));
    setShowSearch(true);
  }, []);

  const handleFindSelection = useCallback(() => {
    const selection = termRef.current?.getSelection();
    if (!selection) return;
    openSearch(selection);
  }, [openSearch]);

  const handlePaste = useCallback(
    (opts?: { suppressNativePaste?: boolean }) => {
      const paste = opts ? pasteFromClipboard(sessionId, opts) : pasteFromClipboard(sessionId);
      paste.catch(console.error);
    },
    [sessionId]
  );

  const handleSelectAll = useCallback(() => {
    termRef.current?.selectAll();
  }, []);

  useEffect(() => {
    const wrapper = wrapperRef.current;
    if (!wrapper) return;

    // 该 effect 故意只依赖 [sessionId]（见末尾 eslint-disable）。effect 内用到的
    // spec 来自 TRANSPORTS（模块级常量），引用稳定，不必进依赖数组。
    const inst = getOrCreateTerminal(sessionId, {
      fontSize,
      fontFamily,
      theme: xtermTheme,
      scrollback,
      transport,
      webglEnabled,
      highlightLinks,
    });
    termRef.current = inst.term;
    fitAddonRef.current = inst.fitAddon;
    searchAddonRef.current = inst.searchAddon;

    // Attach the persistent host into the React-managed wrapper. Xterm content
    // survives because both the host element and the XTerminal live in the
    // registry, not in this component — so split-pane re-renders that unmount
    // this component don't destroy scrollback.
    wrapper.appendChild(inst.container);

    requestAnimationFrame(() => {
      inst.fitAddon.fit();
      // 不依赖 ResizeObserver 首帧 fire 把 PTY 尺寸同步给后端：后端 PTY 创建时
      // cols/rows 是连接请求里硬编码的 80x24，vi 等全屏程序依赖准确 rows，
      // 这里挂载完成立刻补一次，避免初次 fire 被 active 状态或时序错过。
      const dims = inst.fitAddon.proposeDimensions();
      if (dims && dims.cols > 0 && dims.rows > 0) {
        spec.resize(sessionId, dims.cols, dims.rows).catch(console.error);
      }
    });

    inst.bridge.setOnCopy(() => {
      return copySelection();
    });
    inst.bridge.setOnPaste(() => handlePaste({ suppressNativePaste: true }));
    inst.bridge.setOnSelectAll(() => handleSelectAll());
    inst.bridge.setOnFind(() => openSearch());

    const selDispose = inst.term.onSelectionChange(() => {
      const nextSelection = inst.term.getSelection();
      setHasSelection(!!nextSelection);
      if (!nextSelection) {
        setFloatingCopyPosition(null);
        lastAutoCopiedSelectionRef.current = "";
      }
    });
    setHasSelection(!!inst.term.getSelection());

    let resizeTimer = 0;
    const resizeObserver = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(() => {
        inst.fitAddon.fit();
        const dims = inst.fitAddon.proposeDimensions();
        if (dims && dims.cols > 0 && dims.rows > 0) {
          spec.resize(sessionId, dims.cols, dims.rows).catch(console.error);
        }
      }, 50);
    });
    resizeObserver.observe(wrapper);

    return () => {
      clearTimeout(resizeTimer);
      selDispose.dispose();
      resizeObserver.disconnect();
      // If the registry already disposed this session (e.g. closePane / reconnect /
      // tab close ran before this cleanup), the xterm instance is destroyed —
      // skip any term operations and just detach.
      const stillAlive = getTerminalInstance(sessionId) === inst;
      if (stillAlive) {
        // Drop callback closures so toast/setShowSearch can be GC'd;
        // bridge keeps a single handler slot, just reset to no-ops.
        inst.bridge.setOnCopy(() => false);
        inst.bridge.setOnPaste(() => {});
        inst.bridge.setOnSelectAll(() => {});
        inst.bridge.setOnFind(() => {});
      }
      if (inst.container.parentElement === wrapper) {
        wrapper.removeChild(inst.container);
      }
      termRef.current = null;
      fitAddonRef.current = null;
      searchAddonRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  useEffect(() => {
    if (!termRef.current) return;
    termRef.current.options.theme = xtermTheme;
    termRef.current.options.fontSize = fontSize;
    // 加 session-unique sentinel 让每个 terminal 独占 atlas，避免清自己的 atlas
    // 时污染其它 session（详见 terminalFonts.ts 的 withTerminalFontIsolation 注释）。
    termRef.current.options.fontFamily = withTerminalFontIsolation(sessionId, withTerminalFontFallback(fontFamily));
    termRef.current.options.scrollback = scrollback;
    const inst = getTerminalInstance(sessionId);
    inst?.urlHighlighter.setEnabled(highlightLinks);
    inst?.urlHighlighter.setColor(terminalUrlHighlightColor(xtermTheme));
    fitAddonRef.current?.fit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [xtermTheme, fontSize, fontFamily, scrollback, highlightLinks]);

  useEffect(() => {
    const inst = getTerminalInstance(sessionId);
    if (inst) inst.bridge.setShortcuts(shortcuts);
  }, [sessionId, shortcuts]);

  useEffect(() => {
    if (active) {
      requestAnimationFrame(() => {
        fitAddonRef.current?.fit();
        termRef.current?.focus();
      });
    }
  }, [active]);

  useEffect(() => {
    if (!terminalDropEnabled) {
      setIsDragOver(false);
      return;
    }
    return registerTerminalFileDropTarget({
      getRect: () => wrapperRef.current?.getBoundingClientRect(),
      uploadFiles: (paths) => {
        setIsDragOver(false);
        void uploadFilesWithRz(sessionId, paths);
      },
    });
  }, [sessionId, terminalDropEnabled]);

  useEffect(() => {
    const el = wrapperRef.current;
    if (!el || !terminalDropEnabled) return;
    const observer = new MutationObserver(() => {
      setIsDragOver(el.classList.contains("wails-drop-target-active"));
    });
    observer.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, [terminalDropEnabled]);

  const splitPane = useTerminalStore((s) => s.splitPane);
  const reconnect = useTerminalStore((s) => s.reconnect);
  const closePane = useTerminalStore((s) => s.closePane);
  const toggleFileManager = useSFTPStore((s) => s.toggleFileManager);
  const closeTab = useTabStore((s) => s.closeTab);

  const handleTerminalMouseUp = useCallback(
    (event: ReactMouseEvent<HTMLDivElement>) => {
      if ((event.target as HTMLElement).closest("[data-terminal-selection-toolbar]")) return;
      requestAnimationFrame(() => {
        const wrapper = wrapperRef.current;
        const selection = termRef.current?.getSelection();
        setHasSelection(!!selection);
        if (!wrapper || !selection) {
          setFloatingCopyPosition(null);
          return;
        }
        if (copyBehavior === "select-copy-right-paste") {
          if (lastAutoCopiedSelectionRef.current !== selection) copySelection();
          setFloatingCopyPosition(null);
          return;
        }
        if (copyBehavior !== "popover-menu") {
          setFloatingCopyPosition(null);
          return;
        }

        const rect = wrapper.getBoundingClientRect();
        const left = Math.min(Math.max(event.clientX - rect.left, 36), Math.max(36, rect.width - 36));
        const top = Math.min(Math.max(event.clientY - rect.top - 40, 8), Math.max(8, rect.height - 40));
        setFloatingCopyPosition({ left, top });
      });
    },
    [copyBehavior, copySelection]
  );

  const handleTerminalContextMenuCapture = useCallback(
    (event: ReactMouseEvent<HTMLDivElement>) => {
      const selection = termRef.current?.getSelection();
      const action = getTerminalContextMenuAction(copyBehavior, selection);
      if (action === "menu") return;
      event.preventDefault();
      event.stopPropagation();
      if (action === "copy" && selection) {
        copyText(selection);
      } else {
        handlePaste();
      }
      requestAnimationFrame(() => termRef.current?.focus());
    },
    [copyBehavior, copyText, handlePaste]
  );

  return (
    <div className="relative h-full w-full flex flex-col">
      <TerminalSearchBar
        visible={showSearch}
        onClose={() => {
          setShowSearch(false);
          termRef.current?.focus();
        }}
        searchAddon={searchAddonRef.current}
        initialQuery={searchRequest.query}
        initialQueryToken={searchRequest.token}
      />
      <ContextMenu
        onOpenChange={(open) => {
          if (!open) {
            requestAnimationFrame(() => termRef.current?.focus());
          }
        }}
      >
        <ContextMenuTrigger className="flex-1 min-h-0">
          <div
            ref={wrapperRef}
            className="relative h-full w-full"
            onMouseDown={() => setFloatingCopyPosition(null)}
            onMouseUp={handleTerminalMouseUp}
            onContextMenuCapture={handleTerminalContextMenuCapture}
            style={{ padding: "4px", "--wails-drop-target": terminalDropEnabled ? "drop" : undefined } as CSSProperties}
          >
            {hasSelection && floatingCopyPosition && (
              <div
                data-terminal-selection-toolbar
                className="absolute z-20 h-8 gap-1.5 shadow-md"
                style={{
                  left: floatingCopyPosition.left,
                  top: floatingCopyPosition.top,
                  transform: "translateX(-50%)",
                }}
                onMouseDown={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                }}
                onMouseUp={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                }}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                }}
              >
                <div className="flex h-8 items-center rounded-md border bg-popover p-0.5 text-popover-foreground shadow-md">
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="h-7 w-7"
                    title={t("ssh.contextMenu.copy")}
                    aria-label={t("ssh.contextMenu.copy")}
                    onClick={handleCopy}
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="h-7 w-7"
                    title={t("ssh.contextMenu.find")}
                    aria-label={t("ssh.contextMenu.find")}
                    onClick={handleFindSelection}
                  >
                    <Search className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            )}
            {isDragOver && (
              <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center bg-primary/5 border-2 border-dashed border-primary/30 rounded animate-in fade-in-0 duration-150">
                <div className="rounded-md bg-background/90 px-3 py-2 text-xs text-primary shadow-sm">
                  {t("zmodem.dragToUpload")}
                </div>
              </div>
            )}
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onClick={handleCopy} disabled={!hasSelection}>
            {t("ssh.contextMenu.copy")}
            <ContextMenuShortcut>{formatBinding(shortcuts["terminal.copy"])}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuItem onClick={() => handlePaste()}>
            {t("ssh.contextMenu.paste")}
            <ContextMenuShortcut>{formatBinding(shortcuts["terminal.paste"])}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem onClick={handleSelectAll}>
            {t("ssh.contextMenu.selectAll")}
            <ContextMenuShortcut>{formatBinding(shortcuts["terminal.selectAll"])}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuItem onClick={() => openSearch()}>
            {t("ssh.contextMenu.find")}
            <ContextMenuShortcut>{formatBinding(shortcuts["terminal.find"])}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem onClick={() => splitPane(tabId, "horizontal")} disabled={!paneConnected || !spec.canSplit}>
            {t("ssh.session.splitH")}
            <ContextMenuShortcut>{formatBinding(shortcuts["split.horizontal"])}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuItem onClick={() => splitPane(tabId, "vertical")} disabled={!paneConnected || !spec.canSplit}>
            {t("ssh.session.splitV")}
            <ContextMenuShortcut>{formatBinding(shortcuts["split.vertical"])}</ContextMenuShortcut>
          </ContextMenuItem>
          <ContextMenuSeparator />
          {spec.hasDirectorySync && (
            <ContextMenuItem onClick={() => toggleFileManager(tabId)}>{t("ssh.contextMenu.sftp")}</ContextMenuItem>
          )}
          <ContextMenuItem onClick={() => reconnect(tabId)}>{t("ssh.session.reconnect")}</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem onClick={() => closePane(tabId, sessionId)}>
            {t("ssh.contextMenu.closePane")}
          </ContextMenuItem>
          <ContextMenuItem onClick={() => closeTab(tabId)} variant="destructive">
            {t("ssh.contextMenu.closeTab")}
            <ContextMenuShortcut>{formatBinding(shortcuts["tab.close"])}</ContextMenuShortcut>
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    </div>
  );
});
