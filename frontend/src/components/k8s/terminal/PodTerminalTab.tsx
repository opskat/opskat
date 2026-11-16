import { useCallback, useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Search, TerminalSquare } from "lucide-react";
import {
  Button,
  cn,
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "@opskat/ui";
import { ClipboardSetText } from "../../../../wailsjs/runtime/runtime";
import { ReadClipboardText } from "../../../../wailsjs/go/system/System";
import { notifyCopied } from "@/lib/notify";
import { useK8sStore, type K8sTerminalState } from "@/stores/k8sStore";
import { formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore, useXtermTheme } from "@/stores/terminalThemeStore";
import { withTerminalFontFallback, withTerminalFontIsolation } from "@/data/terminalFonts";
import { TerminalSearchBar } from "@/components/terminal/TerminalSearchBar";
import { getTerminalContextMenuAction } from "@/components/terminal/terminalContextMenuPolicy";
import { fitPodTerminal, getPodTerminal } from "./podTerminalRegistry";

interface Props {
  assetId: number;
  tabId: string;
}

/**
 * A Pod terminal inner tab. The xterm and its session live in podTerminalRegistry; this view only mounts the
 * terminal's host element while the tab is shown, so switching tabs keeps the session and its scrollback.
 */
export function PodTerminalTab({ assetId, tabId }: Props) {
  const { t } = useTranslation();
  const state = useK8sStore((s) => s.terminals[assetId]?.[tabId]);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [showSearch, setShowSearch] = useState(false);
  const [searchRequest, setSearchRequest] = useState<{ query: string | null; token: number }>({
    query: null,
    token: 0,
  });
  const xtermTheme = useXtermTheme();
  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const fontFamily = useTerminalThemeStore((s) => s.fontFamily);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const copyBehavior = useTerminalThemeStore((s) => s.copyBehavior);
  const shortcuts = useShortcutStore((s) => s.shortcuts);
  const terminal = getPodTerminal(assetId, tabId);

  const copyText = useCallback(
    (text: string) => {
      ClipboardSetText(text)
        .then(() => notifyCopied(t("ssh.contextMenu.copied")))
        .catch(console.error);
    },
    [t]
  );
  const copySelection = useCallback(() => {
    const selection = getPodTerminal(assetId, tabId)?.term.getSelection();
    if (!selection) return false;
    copyText(selection);
    return true;
  }, [assetId, tabId, copyText]);
  // Same path as the app terminal: the system clipboard via Go, fed through term.paste (CRLF normalisation).
  const paste = useCallback(() => {
    ReadClipboardText()
      .then((text) => {
        if (text) getPodTerminal(assetId, tabId)?.term.paste(text);
      })
      .catch(console.error);
  }, [assetId, tabId]);
  const selectAll = useCallback(() => getPodTerminal(assetId, tabId)?.term.selectAll(), [assetId, tabId]);
  const openSearch = useCallback((query: string | null = null) => {
    setSearchRequest((req) => ({ query, token: req.token + 1 }));
    setShowSearch(true);
  }, []);

  useEffect(() => {
    const wrapper = wrapperRef.current;
    const pt = getPodTerminal(assetId, tabId);
    if (!wrapper || !pt) return;
    wrapper.appendChild(pt.host);
    const frame = requestAnimationFrame(() => {
      fitPodTerminal(assetId, tabId);
      pt.term.focus();
    });
    let resizeTimer = 0;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(() => fitPodTerminal(assetId, tabId), 50);
    });
    observer.observe(wrapper);
    pt.bridge.setOnCopy(copySelection);
    pt.bridge.setOnPaste(paste);
    pt.bridge.setOnSelectAll(selectAll);
    pt.bridge.setOnFind(() => openSearch());
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(resizeTimer);
      observer.disconnect();
      // The terminal may already be gone (its tab closed); otherwise drop the handlers bound to this view.
      if (getPodTerminal(assetId, tabId) === pt) {
        pt.bridge.setOnCopy(() => false);
        pt.bridge.setOnPaste(() => {});
        pt.bridge.setOnSelectAll(() => {});
        pt.bridge.setOnFind(() => {});
      }
      if (pt.host.parentElement === wrapper) wrapper.removeChild(pt.host);
    };
  }, [assetId, tabId, copySelection, paste, selectAll, openSearch]);

  useEffect(() => {
    const pt = getPodTerminal(assetId, tabId);
    if (!pt) return;
    pt.term.options.theme = xtermTheme;
    pt.term.options.fontSize = fontSize;
    pt.term.options.fontFamily = withTerminalFontIsolation(`${assetId}|${tabId}`, withTerminalFontFallback(fontFamily));
    pt.term.options.scrollback = scrollback;
    fitPodTerminal(assetId, tabId);
  }, [assetId, tabId, xtermTheme, fontSize, fontFamily, scrollback]);

  useEffect(() => {
    getPodTerminal(assetId, tabId)?.bridge.setShortcuts(shortcuts);
  }, [assetId, tabId, shortcuts]);

  const handleMouseUp = useCallback(() => {
    if (copyBehavior !== "select-copy-right-paste") return;
    requestAnimationFrame(() => copySelection());
  }, [copyBehavior, copySelection]);

  const handleContextMenuCapture = useCallback(
    (event: ReactMouseEvent<HTMLDivElement>) => {
      const selection = getPodTerminal(assetId, tabId)?.term.getSelection();
      const action = getTerminalContextMenuAction(copyBehavior, selection);
      if (action === "menu") return;
      event.preventDefault();
      event.stopPropagation();
      if (action === "copy" && selection) copyText(selection);
      else paste();
    },
    [assetId, tabId, copyBehavior, copyText, paste]
  );

  if (!state) return null;

  return (
    <div className="flex h-full flex-col" data-testid="k8s-pod-terminal">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3 text-xs">
        <TerminalSquare className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="truncate font-mono font-medium">
          {state.namespace}/{state.pod}
        </span>
        <span className="text-muted-foreground">{t("asset.k8sTerminalContainer")}</span>
        <span className="rounded-md border px-2 py-1 font-mono">{state.container}</span>
        <span className="text-muted-foreground">Shell</span>
        <span className={cn("rounded-md border px-2 py-1", state.shell !== "auto" && "font-mono")}>
          {state.shell === "auto" ? t("asset.k8sTerminalShellAuto") : state.shell}
        </span>
        <StatusBadge status={state.status} />
        <div className="ml-auto flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            title={`${t("ssh.contextMenu.find")} (${formatBinding(shortcuts["terminal.find"])})`}
            aria-label={t("ssh.contextMenu.find")}
            onClick={() => openSearch()}
          >
            <Search className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
      <div className="relative flex min-h-0 flex-1 flex-col">
        <TerminalSearchBar
          visible={showSearch}
          onClose={() => {
            setShowSearch(false);
            terminal?.term.focus();
          }}
          searchAddon={terminal?.searchAddon ?? null}
          initialQuery={searchRequest.query}
          initialQueryToken={searchRequest.token}
        />
        <ContextMenu
          onOpenChange={(open) => {
            if (!open) requestAnimationFrame(() => terminal?.term.focus());
          }}
        >
          <ContextMenuTrigger className="min-h-0 flex-1">
            <div
              ref={wrapperRef}
              className="h-full w-full p-1"
              onMouseUp={handleMouseUp}
              onContextMenuCapture={handleContextMenuCapture}
            />
          </ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuItem onClick={() => copySelection()}>
              {t("ssh.contextMenu.copy")}
              <ContextMenuShortcut>{formatBinding(shortcuts["terminal.copy"])}</ContextMenuShortcut>
            </ContextMenuItem>
            <ContextMenuItem onClick={paste}>
              {t("ssh.contextMenu.paste")}
              <ContextMenuShortcut>{formatBinding(shortcuts["terminal.paste"])}</ContextMenuShortcut>
            </ContextMenuItem>
            <ContextMenuSeparator />
            <ContextMenuItem onClick={selectAll}>
              {t("ssh.contextMenu.selectAll")}
              <ContextMenuShortcut>{formatBinding(shortcuts["terminal.selectAll"])}</ContextMenuShortcut>
            </ContextMenuItem>
            <ContextMenuItem onClick={() => openSearch()}>
              {t("ssh.contextMenu.find")}
              <ContextMenuShortcut>{formatBinding(shortcuts["terminal.find"])}</ContextMenuShortcut>
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: K8sTerminalState["status"] }) {
  const { t } = useTranslation();
  if (status === "connecting") {
    return (
      <span className="ml-2 flex items-center gap-1 text-warning">
        <Loader2 className="h-3 w-3 animate-spin" />
        {t("asset.k8sTerminalConnecting")}
      </span>
    );
  }
  const connected = status === "connected";
  return (
    <span className={cn("ml-2 flex items-center gap-1", connected ? "text-success" : "text-muted-foreground")}>
      <span className={cn("h-1.5 w-1.5 rounded-full", connected ? "bg-success" : "bg-muted-foreground")} />
      {connected ? t("asset.k8sTerminalConnected") : t("asset.k8sTerminalDisconnected")}
    </span>
  );
}
