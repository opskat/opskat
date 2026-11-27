import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Loader2, RotateCcw, Search, TerminalSquare, Unplug } from "lucide-react";
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
import { TerminalSearchBar } from "@/components/terminal/TerminalSearchBar";
import { TerminalSelectionToolbar } from "@/components/terminal/TerminalSelectionToolbar";
import {
  TERMINAL_COPIED_TOAST_ID,
  useTerminalMouseInteraction,
} from "@/components/terminal/useTerminalMouseInteraction";
import {
  disconnectPodTerminal,
  fitPodTerminal,
  getPodTerminal,
  podTerminalFontFamily,
  restartPodTerminal,
} from "./podTerminalRegistry";
import { endText } from "./terminalText";

const PRESET_SHELLS = ["auto", "/bin/bash", "/bin/sh"];
const CUSTOM_SHELL = "custom";
const selectClass = "h-7 rounded-md border bg-background px-2 text-xs";

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
  // "Custom command…" picked, or a custom command already running: the command input is shown.
  const [customMode, setCustomMode] = useState(() => !!state && !PRESET_SHELLS.includes(state.shell));
  const [customDraft, setCustomDraft] = useState(() =>
    state && !PRESET_SHELLS.includes(state.shell) ? state.shell : ""
  );
  const [searchRequest, setSearchRequest] = useState<{ query: string | null; token: number }>({
    query: null,
    token: 0,
  });
  const [hasSelection, setHasSelection] = useState(false);
  const [floatingCopyPosition, setFloatingCopyPosition] = useState<{ left: number; top: number } | null>(null);
  const xtermTheme = useXtermTheme();
  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const fontFamily = useTerminalThemeStore((s) => s.fontFamily);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const shortcuts = useShortcutStore((s) => s.shortcuts);
  const terminal = getPodTerminal(assetId, tabId);

  const copyText = useCallback(
    (text: string) => {
      ClipboardSetText(text)
        .then(() => notifyCopied(t("ssh.contextMenu.copied"), { id: TERMINAL_COPIED_TOAST_ID }))
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
  const clearTerminal = useCallback(() => getPodTerminal(assetId, tabId)?.term.clear(), [assetId, tabId]);
  const openSearch = useCallback((query: string | null = null) => {
    setSearchRequest((req) => ({ query, token: req.token + 1 }));
    setShowSearch(true);
  }, []);
  const handleFindSelection = useCallback(() => {
    const selection = getPodTerminal(assetId, tabId)?.term.getSelection();
    if (!selection) return;
    openSearch(selection);
  }, [assetId, tabId, openSearch]);

  const { onMouseDownCapture, onContextMenuCapture, onAuxClickCapture, resetLastAutoCopied } =
    useTerminalMouseInteraction({
      wrapperRef,
      getTerm: () => getPodTerminal(assetId, tabId)?.term,
      onCopy: copySelection,
      onCopyText: copyText,
      onPaste: paste,
      onClear: clearTerminal,
      onSelectionPresenceChange: setHasSelection,
      onFloatingPopoverChange: setFloatingCopyPosition,
    });

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
    const selDispose = pt.term.onSelectionChange(() => {
      const nextSelection = pt.term.getSelection();
      setHasSelection(!!nextSelection);
      if (!nextSelection) {
        setFloatingCopyPosition(null);
        resetLastAutoCopied();
      }
    });
    setHasSelection(!!pt.term.getSelection());
    return () => {
      cancelAnimationFrame(frame);
      clearTimeout(resizeTimer);
      selDispose.dispose();
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
  }, [assetId, tabId, copySelection, paste, selectAll, openSearch, resetLastAutoCopied]);

  useEffect(() => {
    const pt = getPodTerminal(assetId, tabId);
    if (!pt) return;
    pt.term.options.theme = xtermTheme;
    pt.term.options.fontSize = fontSize;
    pt.term.options.fontFamily = podTerminalFontFamily(assetId, tabId, fontFamily);
    pt.term.options.scrollback = scrollback;
    fitPodTerminal(assetId, tabId);
  }, [assetId, tabId, xtermTheme, fontSize, fontFamily, scrollback]);

  useEffect(() => {
    getPodTerminal(assetId, tabId)?.bridge.setShortcuts(shortcuts);
  }, [assetId, tabId, shortcuts]);

  if (!state) return null;

  return (
    <div className="flex h-full flex-col" data-testid="k8s-pod-terminal">
      <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b px-3 py-1 text-xs">
        <TerminalSquare className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="truncate font-mono font-medium">
          {state.namespace}/{state.pod}
        </span>
        <span className="text-muted-foreground">{t("asset.k8sTerminalContainer")}</span>
        <select
          aria-label={t("asset.k8sTerminalContainer")}
          className={cn(selectClass, "font-mono")}
          value={state.container}
          onChange={(e) => {
            if (e.target.value !== state.container) restartPodTerminal(assetId, tabId, { container: e.target.value });
          }}
        >
          {state.containers.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
        <span className="text-muted-foreground">{t("asset.k8sTerminalShell")}</span>
        <select
          aria-label={t("asset.k8sTerminalShell")}
          className={selectClass}
          value={customMode ? CUSTOM_SHELL : state.shell}
          onChange={(e) => {
            const value = e.target.value;
            if (value === CUSTOM_SHELL) {
              setCustomMode(true);
              return;
            }
            setCustomMode(false);
            if (value !== state.shell) restartPodTerminal(assetId, tabId, { shell: value });
          }}
        >
          <option value="auto">{t("asset.k8sTerminalShellAuto")}</option>
          <option value="/bin/bash">/bin/bash</option>
          <option value="/bin/sh">/bin/sh</option>
          <option value={CUSTOM_SHELL}>{t("asset.k8sTerminalShellCustom")}</option>
        </select>
        {customMode && (
          <input
            aria-label={t("asset.k8sTerminalCustomCommand")}
            className="h-7 w-56 rounded-md border bg-background px-2 font-mono text-xs"
            placeholder={t("asset.k8sTerminalCustomPlaceholder")}
            value={customDraft}
            onChange={(e) => setCustomDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && customDraft.trim())
                restartPodTerminal(assetId, tabId, { shell: customDraft.trim() });
            }}
          />
        )}
        <StatusBadge status={state.status} shell={state.shellUsed} />
        <div className="ml-auto flex shrink-0 items-center gap-1">
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
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-1 text-xs"
            onClick={() => restartPodTerminal(assetId, tabId)}
          >
            <RotateCcw className="h-3.5 w-3.5" />
            {t("asset.k8sTerminalReconnect")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs"
            disabled={state.status === "ended"}
            onClick={() => disconnectPodTerminal(assetId, tabId)}
          >
            <Unplug className="h-3.5 w-3.5" />
            {t("asset.k8sTerminalDisconnect")}
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
              className="relative h-full w-full p-1"
              onMouseDownCapture={onMouseDownCapture}
              onContextMenuCapture={onContextMenuCapture}
              onAuxClickCapture={onAuxClickCapture}
            >
              {hasSelection && floatingCopyPosition && (
                <TerminalSelectionToolbar
                  position={floatingCopyPosition}
                  onCopy={() => copySelection()}
                  onFind={handleFindSelection}
                />
              )}
            </div>
          </ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuItem onClick={() => copySelection()} disabled={!hasSelection}>
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
            <ContextMenuItem onClick={clearTerminal}>{t("ssh.contextMenu.clear")}</ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
        {state.status === "ended" && state.end && (
          <div
            data-testid="k8s-pod-terminal-ended"
            className="absolute inset-x-0 bottom-0 flex items-center gap-3 border-t border-white/10 bg-black/70 px-4 py-2 text-xs text-white"
          >
            <AlertTriangle className="h-4 w-4 shrink-0 text-warning" />
            <span className="min-w-0 flex-1 break-words">{endText(state.end)}</span>
            <Button size="sm" className="h-7 gap-1 text-xs" onClick={() => restartPodTerminal(assetId, tabId)}>
              <RotateCcw className="h-3.5 w-3.5" />
              {t("asset.k8sTerminalReconnect")}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

function StatusBadge({ status, shell }: { status: K8sTerminalState["status"]; shell: string | null }) {
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
      {connected && shell && <span className="font-mono text-muted-foreground">· {shell}</span>}
    </span>
  );
}
