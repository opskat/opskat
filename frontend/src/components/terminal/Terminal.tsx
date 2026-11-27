import { useCallback, useEffect, useImperativeHandle, useRef, useState, type CSSProperties, type Ref } from "react";
import type { Terminal as XTerminal } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";
import { ClipboardSetText } from "../../../wailsjs/runtime/runtime";
import { useShortcutStore, formatBinding } from "@/stores/shortcutStore";
import { useTerminalStore, TRANSPORTS } from "@/stores/terminalStore";
import { useTerminalThemeStore, useXtermTheme } from "@/stores/terminalThemeStore";
import { withTerminalFontFallback, withTerminalFontIsolation } from "@/data/terminalFonts";
import { useTranslation } from "react-i18next";
import { notifyCopied } from "@/lib/notify";
import {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
} from "@opskat/ui";
import { TerminalSearchBar } from "./TerminalSearchBar";
import { TerminalSelectionToolbar } from "./TerminalSelectionToolbar";
import { TERMINAL_COPIED_TOAST_ID, useTerminalMouseInteraction } from "./useTerminalMouseInteraction";
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
  ref?: Ref<TerminalHandle>;
}

export function Terminal({ sessionId, active, tabId, ref }: TerminalProps) {
  const { t } = useTranslation();
  const wrapperRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [showSearch, setShowSearch] = useState(false);
  const [searchRequest, setSearchRequest] = useState<{ query: string | null; token: number }>({
    query: null,
    token: 0,
  });
  const [hasSelection, setHasSelection] = useState(false);
  const [floatingCopyPosition, setFloatingCopyPosition] = useState<{ left: number; top: number } | null>(null);
  const [isDragOver, setIsDragOver] = useState(false);
  const shortcuts = useShortcutStore((s) => s.shortcuts);
  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const fontFamily = useTerminalThemeStore((s) => s.fontFamily);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const webglEnabled = useTerminalThemeStore((s) => s.webglEnabled);
  const highlightLinks = useTerminalThemeStore((s) => s.highlightLinks);
  const osc52Clipboard = useTerminalThemeStore((s) => s.osc52ClipboardEnabled);
  const xtermTheme = useXtermTheme();
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
        .then(() => notifyCopied(t("ssh.contextMenu.copied"), { id: TERMINAL_COPIED_TOAST_ID }))
        .catch(console.error);
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

  const handleClear = useCallback(() => {
    termRef.current?.clear();
  }, []);

  const splitPane = useTerminalStore((s) => s.splitPane);
  const reconnect = useTerminalStore((s) => s.reconnect);
  const closePane = useTerminalStore((s) => s.closePane);
  const setActivePaneId = useTerminalStore((s) => s.setActivePaneId);
  const toggleFileManager = useSFTPStore((s) => s.toggleFileManager);
  const closeTab = useTabStore((s) => s.closeTab);

  const handleFocusPane = useCallback(() => {
    setActivePaneId(tabId, sessionId);
  }, [setActivePaneId, tabId, sessionId]);

  const handleReconnect = useCallback(() => {
    setActivePaneId(tabId, sessionId);
    reconnect(tabId);
  }, [setActivePaneId, reconnect, tabId, sessionId]);

  const { onMouseDownCapture, onContextMenuCapture, onAuxClickCapture, resetLastAutoCopied } =
    useTerminalMouseInteraction({
      wrapperRef,
      getTerm: () => termRef.current,
      onCopy: copySelection,
      onCopyText: copyText,
      onPaste: () => handlePaste(),
      onClear: handleClear,
      onSelectionPresenceChange: setHasSelection,
      onFloatingPopoverChange: setFloatingCopyPosition,
      onFocusPane: handleFocusPane,
    });

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
      osc52Clipboard,
    });
    termRef.current = inst.term;
    fitAddonRef.current = inst.fitAddon;

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
        resetLastAutoCopied();
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
    inst?.clipboardOsc52.setEnabled(osc52Clipboard);
    fitAddonRef.current?.fit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [xtermTheme, fontSize, fontFamily, scrollback, highlightLinks, osc52Clipboard]);

  useEffect(() => {
    const inst = getTerminalInstance(sessionId);
    if (inst) inst.bridge.setShortcuts(shortcuts);
  }, [sessionId, shortcuts]);

  useEffect(() => {
    // 隐藏标签暂停 URL 扫描；激活时对当前视口补扫一次（数据照常写入缓冲）。
    const inst = getTerminalInstance(sessionId);
    inst?.urlHighlighter.setActive(active);
    // visibility:hidden 仍与视口相交，xterm 的 IntersectionObserver 不会暂停渲染；
    // 非激活时把宿主平移出视口（保留盒尺寸，fit 仍可用），xterm 自身即暂停绘制，
    // 恢复相交时它会对暂停期间变更的行整屏刷新（RenderService._handleIntersectionChange）。
    if (inst) inst.container.style.transform = active ? "" : "translateX(-100000px)";
    if (active) {
      requestAnimationFrame(() => {
        fitAddonRef.current?.fit();
        termRef.current?.focus();
      });
    }
  }, [active, sessionId]);

  // drop 不可用时清掉拖拽高亮:渲染期自终止守卫,替代 effect 里的同步 setState
  if (!terminalDropEnabled && isDragOver) {
    setIsDragOver(false);
  }

  useEffect(() => {
    if (!terminalDropEnabled) return;
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

  return (
    <div className="relative h-full w-full flex flex-col">
      <TerminalSearchBar
        visible={showSearch}
        onClose={() => {
          setShowSearch(false);
          termRef.current?.focus();
        }}
        searchAddon={getTerminalInstance(sessionId)?.searchAddon ?? null}
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
            data-testid={`terminal-${sessionId}`}
            className="relative h-full w-full"
            onMouseDownCapture={onMouseDownCapture}
            onContextMenuCapture={onContextMenuCapture}
            onAuxClickCapture={onAuxClickCapture}
            style={{ padding: "4px", "--wails-drop-target": terminalDropEnabled ? "drop" : undefined } as CSSProperties}
          >
            {hasSelection && floatingCopyPosition && (
              <TerminalSelectionToolbar
                position={floatingCopyPosition}
                onCopy={handleCopy}
                onFind={handleFindSelection}
              />
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
          <ContextMenuItem onClick={handleClear}>{t("ssh.contextMenu.clear")}</ContextMenuItem>
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
          <ContextMenuItem onClick={handleReconnect}>{t("ssh.session.reconnect")}</ContextMenuItem>
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
}
