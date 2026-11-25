import { useCallback, useEffect, useRef, type MouseEvent as ReactMouseEvent, type RefObject } from "react";
import type { Terminal as XTerminal } from "@xterm/xterm";
import { isMac, useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { dispatchAppAction } from "@/lib/appActionDispatch";
import {
  isMacCtrlLeftContextMenu,
  isMouseDrag,
  resolveTerminalMousePolicy,
  shouldAutoCopySelection,
  shouldPreventMouseDownDefault,
  type ExecutableTerminalMouseAction,
  type MouseEventSnapshot,
  type MouseTrackingMode,
} from "./terminalMousePolicy";

export const TERMINAL_COPIED_TOAST_ID = "terminal-copied";

export interface UseTerminalMouseInteractionOptions {
  wrapperRef: RefObject<HTMLDivElement | null>;
  getTerm: () => XTerminal | null | undefined;
  readOnly?: boolean;
  onCopy: () => boolean;
  onCopyText: (text: string) => void;
  onPaste?: () => void;
  onClear?: () => void;
  onSelectionPresenceChange?: (hasSelection: boolean) => void;
  onFloatingPopoverChange?: (pos: { left: number; top: number } | null) => void;
  onFocusPane?: () => void;
}

interface PendingPress {
  button: number;
  x: number;
  y: number;
}

function shouldRefocusTerminalAfterAction(action: ExecutableTerminalMouseAction): boolean {
  return action === "terminal.copy" || action === "terminal.paste" || action === "terminal.clear";
}

function readMouseTrackingMode(term: XTerminal | null | undefined): MouseTrackingMode {
  return term?.modes?.mouseTrackingMode ?? "none";
}

function snapshotFromEvent(
  e: Pick<MouseEvent, "button" | "metaKey" | "ctrlKey" | "shiftKey" | "altKey">
): MouseEventSnapshot {
  return {
    button: e.button,
    metaKey: e.metaKey,
    ctrlKey: e.ctrlKey,
    shiftKey: e.shiftKey,
    altKey: e.altKey,
  };
}

/**
 * Shared terminal mouse interaction & text-selection hook.
 * Used uniformly by SSH/Local/Serial (`Terminal.tsx`), K8s Pod Exec (`PodTerminalTab.tsx`),
 * and K8s Pod Log (`K8sLogTerminal.tsx`, `readOnly: true`).
 */
export function useTerminalMouseInteraction(opts: UseTerminalMouseInteractionOptions) {
  const selectionAction = useTerminalThemeStore((s) => s.selectionAction);
  const mouseShortcuts = useShortcutStore((s) => s.mouseShortcuts);

  const pressRef = useRef<PendingPress | null>(null);
  const lastAutoCopiedRef = useRef("");
  const syntheticContextMenuRef = useRef(false);
  const activeMouseUpCleanupRef = useRef<(() => void) | null>(null);
  const rightPressCleanupRef = useRef<(() => void) | null>(null);
  const handledAuxButtonRef = useRef<number | null>(null);

  // Keep latest callbacks and state accessible from window-level one-shot listeners without re-binding.
  const stateRef = useRef({
    opts,
    selectionAction,
    mouseShortcuts,
  });
  useEffect(() => {
    stateRef.current = { opts, selectionAction, mouseShortcuts };
  });

  // Dismiss any visible selection popover immediately when settings change in background tabs.
  useEffect(() => {
    if (selectionAction !== "popover") {
      stateRef.current.opts.onFloatingPopoverChange?.(null);
    }
  }, [selectionAction]);

  useEffect(() => {
    return () => {
      activeMouseUpCleanupRef.current?.();
      activeMouseUpCleanupRef.current = null;
      rightPressCleanupRef.current?.();
      rightPressCleanupRef.current = null;
      pressRef.current = null;
    };
  }, []);

  const resetLastAutoCopied = useCallback(() => {
    lastAutoCopiedRef.current = "";
  }, []);

  const executeAction = useCallback((action: ExecutableTerminalMouseAction) => {
    const { opts: cur } = stateRef.current;
    switch (action) {
      case "terminal.copy":
        cur.onCopy();
        return;
      case "terminal.paste":
        cur.onPaste?.();
        return;
      case "terminal.clear":
        cur.onClear?.();
        return;
      case "tab.prev":
      case "tab.next":
      case "tab.close":
        cur.onFocusPane?.();
        dispatchAppAction(action);
    }
  }, []);

  const openSyntheticContextMenu = useCallback((clientX: number, clientY: number) => {
    const wrapper = stateRef.current.opts.wrapperRef.current;
    if (!wrapper) return;
    syntheticContextMenuRef.current = true;
    try {
      wrapper.dispatchEvent(
        new MouseEvent("contextmenu", {
          bubbles: true,
          cancelable: true,
          clientX,
          clientY,
          button: 2,
        })
      );
    } finally {
      syntheticContextMenuRef.current = false;
    }
  }, []);

  const finishSelectionAfterLeftMouseUp = useCallback((clientX: number, clientY: number) => {
    requestAnimationFrame(() => {
      const { opts: cur, selectionAction: mode } = stateRef.current;
      const wrapper = cur.wrapperRef.current;
      const term = cur.getTerm();
      const selection = term?.getSelection() ?? "";
      cur.onSelectionPresenceChange?.(!!selection);

      if (!wrapper || !selection) {
        cur.onFloatingPopoverChange?.(null);
        if (!selection) lastAutoCopiedRef.current = "";
        return;
      }

      if (mode === "auto-copy") {
        cur.onFloatingPopoverChange?.(null);
        const toCopy = shouldAutoCopySelection(selection, mode, lastAutoCopiedRef.current);
        if (toCopy) {
          lastAutoCopiedRef.current = toCopy;
          cur.onCopyText(toCopy);
          term?.clearSelection();
          cur.onSelectionPresenceChange?.(false);
        }
        return;
      }

      if (mode !== "popover") {
        cur.onFloatingPopoverChange?.(null);
        return;
      }

      const rect = wrapper.getBoundingClientRect();
      const left = Math.min(Math.max(clientX - rect.left, 36), Math.max(36, rect.width - 36));
      const top = Math.min(Math.max(clientY - rect.top - 40, 8), Math.max(8, rect.height - 40));
      cur.onFloatingPopoverChange?.({ left, top });
    });
  }, []);

  const onMouseDownCapture = useCallback(
    (event: ReactMouseEvent<HTMLDivElement>) => {
      if ((event.target as HTMLElement | null)?.closest("[data-terminal-selection-toolbar]")) return;

      const { opts: cur, mouseShortcuts: bindings } = stateRef.current;
      cur.onFloatingPopoverChange?.(null);

      const term = cur.getTerm();
      const snapshot = snapshotFromEvent(event.nativeEvent);
      const policyCtx = {
        bindings,
        mouseTrackingMode: readMouseTrackingMode(term),
        hasSelection: Boolean(term?.getSelection()),
        readOnly: Boolean(cur.readOnly),
      };

      const press: PendingPress = {
        button: event.button,
        x: event.clientX,
        y: event.clientY,
      };
      pressRef.current = press;

      if (shouldPreventMouseDownDefault(snapshot, policyCtx)) {
        // Stopping propagation prevents SplitPane's onMouseDown from updating activePaneId,
        // so notify the pane owner directly to keep split-pane focus synchronized.
        cur.onFocusPane?.();
        event.preventDefault();
        event.stopPropagation();
      }

      // Right-click (button 2) is decided and executed exclusively in onContextMenuCapture
      // so macOS (contextmenu on mousedown) and Windows/Linux (contextmenu on mouseup) never double-fire.
      // Attach a one-shot window mouseup/blur cleanup so releasing outside the terminal never leaves
      // a stale pressRef behind for later keyboard ContextMenu / Shift+F10 events.
      if (event.button === 2) {
        rightPressCleanupRef.current?.();
        let clearTimer: number | null = null;
        const cleanupRightPress = () => {
          if (clearTimer !== null) {
            window.clearTimeout(clearTimer);
            clearTimer = null;
          }
          window.removeEventListener("mouseup", handleRightMouseUp, true);
          window.removeEventListener("blur", handleRightBlur);
          if (rightPressCleanupRef.current === cleanupRightPress) {
            rightPressCleanupRef.current = null;
          }
        };
        const clearStalePress = () => {
          if (pressRef.current === press) {
            pressRef.current = null;
          }
          cleanupRightPress();
        };
        const handleRightMouseUp = (upEvent: MouseEvent) => {
          if (upEvent.button !== 2) return;
          window.removeEventListener("mouseup", handleRightMouseUp, true);
          window.removeEventListener("blur", handleRightBlur);
          // Defer one tick so Windows/Linux's synchronous post-mouseup contextmenu on the terminal
          // reads pressRef first, while off-terminal releases clear it cleanly.
          clearTimer = window.setTimeout(clearStalePress, 0);
        };
        const handleRightBlur = () => {
          clearStalePress();
        };
        rightPressCleanupRef.current = cleanupRightPress;
        window.addEventListener("mouseup", handleRightMouseUp, true);
        window.addEventListener("blur", handleRightBlur);
        return;
      }

      // Replace any previous pending window mouseup/right-press listeners.
      rightPressCleanupRef.current?.();
      activeMouseUpCleanupRef.current?.();
      activeMouseUpCleanupRef.current = null;

      const handleWindowMouseUp = (upEvent: MouseEvent) => {
        if (upEvent.button !== press.button) return;
        cleanup();

        const dragged = isMouseDrag(press.x, press.y, upEvent.clientX, upEvent.clientY);
        if (pressRef.current === press) {
          pressRef.current = null;
        }

        if (upEvent.button === 0) {
          finishSelectionAfterLeftMouseUp(upEvent.clientX, upEvent.clientY);
          return;
        }

        // Middle (1) and Side buttons (3, 4): dispatch on mouseup (not auxclick), guarded by drag threshold.
        if (upEvent.button === 1 || upEvent.button === 3 || upEvent.button === 4) {
          if (dragged) return;
          const { opts: latestOpts, mouseShortcuts: latestBindings } = stateRef.current;
          const latestTerm = latestOpts.getTerm();
          const outcome = resolveTerminalMousePolicy(snapshotFromEvent(upEvent), {
            bindings: latestBindings,
            mouseTrackingMode: readMouseTrackingMode(latestTerm),
            hasSelection: Boolean(latestTerm?.getSelection()),
            readOnly: Boolean(latestOpts.readOnly),
            wasDragged: false,
          });
          if (outcome.kind === "action") {
            handledAuxButtonRef.current = upEvent.button;
            upEvent.preventDefault();
            executeAction(outcome.action);
            if (shouldRefocusTerminalAfterAction(outcome.action)) {
              requestAnimationFrame(() => latestTerm?.focus());
            }
          } else if (outcome.kind === "contextMenu") {
            handledAuxButtonRef.current = upEvent.button;
            upEvent.preventDefault();
            openSyntheticContextMenu(upEvent.clientX, upEvent.clientY);
          }
        }
      };

      const cleanup = () => {
        window.removeEventListener("mouseup", handleWindowMouseUp, true);
        if (activeMouseUpCleanupRef.current === cleanup) {
          activeMouseUpCleanupRef.current = null;
        }
      };
      activeMouseUpCleanupRef.current = cleanup;
      window.addEventListener("mouseup", handleWindowMouseUp, true);
    },
    [executeAction, finishSelectionAfterLeftMouseUp, openSyntheticContextMenu]
  );

  const onAuxClickCapture = useCallback((event: ReactMouseEvent<HTMLDivElement>) => {
    if (event.button === 3 || event.button === 4 || handledAuxButtonRef.current === event.button) {
      if (handledAuxButtonRef.current === event.button) {
        handledAuxButtonRef.current = null;
      }
      event.preventDefault();
      event.stopPropagation();
    }
  }, []);

  const onContextMenuCapture = useCallback(
    (event: ReactMouseEvent<HTMLDivElement>) => {
      if (syntheticContextMenuRef.current) return;
      if ((event.target as HTMLElement | null)?.closest("[data-terminal-selection-toolbar]")) return;

      const { opts: cur, mouseShortcuts: bindings } = stateRef.current;
      const term = cur.getTerm();
      const press = pressRef.current;

      // On macOS, Control + Left Click fires a synthetic contextmenu right after mousedown.
      // Never route it to button-2 (Right Click) bindings; only suppress over TUI without Shift.
      if (isMacCtrlLeftContextMenu(event.nativeEvent, press?.button ?? null, isMac)) {
        if (readMouseTrackingMode(term) !== "none" && !event.shiftKey) {
          event.preventDefault();
          event.stopPropagation();
        }
        return;
      }

      const dragged = press?.button === 2 ? isMouseDrag(press.x, press.y, event.clientX, event.clientY) : false;
      if (press?.button === 2) {
        pressRef.current = null;
        rightPressCleanupRef.current?.();
      }

      const rightSnapshot: MouseEventSnapshot = {
        button: 2,
        metaKey: event.metaKey,
        ctrlKey: event.ctrlKey,
        shiftKey: event.shiftKey,
        altKey: event.altKey,
      };
      const outcome = resolveTerminalMousePolicy(rightSnapshot, {
        bindings,
        mouseTrackingMode: readMouseTrackingMode(term),
        hasSelection: Boolean(term?.getSelection()),
        readOnly: Boolean(cur.readOnly),
        wasDragged: dragged,
      });

      if (outcome.kind === "contextMenu") {
        return;
      }

      event.preventDefault();
      event.stopPropagation();

      if (outcome.kind === "action") {
        executeAction(outcome.action);
        if (shouldRefocusTerminalAfterAction(outcome.action)) {
          requestAnimationFrame(() => term?.focus());
        }
      }
    },
    [executeAction]
  );

  return {
    onMouseDownCapture,
    onContextMenuCapture,
    onAuxClickCapture,
    resetLastAutoCopied,
  };
}
