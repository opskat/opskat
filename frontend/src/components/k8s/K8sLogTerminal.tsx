import { useCallback, useEffect, useImperativeHandle, useRef, useState, type Ref } from "react";
import { useTranslation } from "react-i18next";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "@opskat/ui";
import { ClipboardSetText } from "../../../wailsjs/runtime/runtime";
import { notifyCopied } from "@/lib/notify";
import { formatBinding, useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore, useXtermTheme } from "@/stores/terminalThemeStore";
import { TerminalSelectionToolbar } from "@/components/terminal/TerminalSelectionToolbar";
import {
  TERMINAL_COPIED_TOAST_ID,
  useTerminalMouseInteraction,
} from "@/components/terminal/useTerminalMouseInteraction";

export interface K8sLogTerminalHandle {
  write: (data: string | Uint8Array) => void;
  clear: () => void;
}

export function K8sLogTerminal({ ref }: { ref?: Ref<K8sLogTerminalHandle> }) {
  const { t } = useTranslation();
  const wrapperRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [hasSelection, setHasSelection] = useState(false);
  const [floatingCopyPosition, setFloatingCopyPosition] = useState<{ left: number; top: number } | null>(null);

  const fontSize = useTerminalThemeStore((s) => s.fontSize);
  const scrollback = useTerminalThemeStore((s) => s.scrollback);
  const shortcuts = useShortcutStore((s) => s.shortcuts);
  const xtermTheme = useXtermTheme();

  const copyText = useCallback(
    (selection: string) => {
      ClipboardSetText(selection)
        .then(() => notifyCopied(t("ssh.contextMenu.copied"), { id: TERMINAL_COPIED_TOAST_ID }))
        .catch(console.error);
    },
    [t]
  );

  const copySelection = useCallback(() => {
    const selection = termRef.current?.getSelection();
    if (!selection) return false;
    copyText(selection);
    return true;
  }, [copyText]);

  const handleSelectAll = useCallback(() => {
    termRef.current?.selectAll();
  }, []);

  const { onMouseDownCapture, onContextMenuCapture, onAuxClickCapture, resetLastAutoCopied } =
    useTerminalMouseInteraction({
      wrapperRef,
      getTerm: () => termRef.current,
      readOnly: true,
      onCopy: copySelection,
      onCopyText: copyText,
      onSelectionPresenceChange: setHasSelection,
      onFloatingPopoverChange: setFloatingCopyPosition,
    });

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

    const selDispose = term.onSelectionChange(() => {
      const nextSelection = term.getSelection();
      setHasSelection(!!nextSelection);
      if (!nextSelection) {
        setFloatingCopyPosition(null);
        resetLastAutoCopied();
      }
    });

    const resizeObserver = new ResizeObserver(() => {
      fitAddon.fit();
    });
    resizeObserver.observe(wrapper);

    return () => {
      selDispose.dispose();
      resizeObserver.disconnect();
      term.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [resetLastAutoCopied]);

  useEffect(() => {
    if (!termRef.current) return;
    termRef.current.options.theme = xtermTheme;
    termRef.current.options.fontSize = fontSize;
    termRef.current.options.scrollback = scrollback;
    fitAddonRef.current?.fit();
  }, [xtermTheme, fontSize, scrollback]);

  return (
    <ContextMenu
      onOpenChange={(open) => {
        if (!open) requestAnimationFrame(() => termRef.current?.focus());
      }}
    >
      <ContextMenuTrigger className="flex flex-1 min-h-0 w-full">
        <div
          ref={wrapperRef}
          data-testid="k8s-log-terminal"
          className="relative flex-1 w-full rounded-lg overflow-hidden min-h-0"
          onMouseDownCapture={onMouseDownCapture}
          onContextMenuCapture={onContextMenuCapture}
          onAuxClickCapture={onAuxClickCapture}
        >
          {hasSelection && floatingCopyPosition && (
            <TerminalSelectionToolbar position={floatingCopyPosition} onCopy={() => copySelection()} />
          )}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onClick={() => copySelection()} disabled={!hasSelection}>
          {t("ssh.contextMenu.copy")}
          <ContextMenuShortcut>{formatBinding(shortcuts["terminal.copy"])}</ContextMenuShortcut>
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onClick={handleSelectAll}>
          {t("ssh.contextMenu.selectAll")}
          <ContextMenuShortcut>{formatBinding(shortcuts["terminal.selectAll"])}</ContextMenuShortcut>
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
