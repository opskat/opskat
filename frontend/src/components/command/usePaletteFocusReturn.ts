import { useCallback, useLayoutEffect, useRef } from "react";
import { useTabStore } from "@/stores/tabStore";

/**
 * Close-autofocus handler for the command palette's hosts (top-bar Popover / fallback Dialog).
 *
 * Radix returns focus to the host's trigger on close (Popover → the search button, Dialog
 * without trigger → body), which strands the keyboard after a snippet is inserted into the
 * terminal the user was typing in (#354). Instead: when the palette closes without switching
 * tabs (snippet landed in the active tab, Escape, re-selecting the active tab), give focus back
 * to the element that had it when the palette opened. When a different tab became active, or
 * something else already took focus (e.g. the snippet host-picker drawer), leave it alone — the
 * newly active tab focuses itself.
 */
export function usePaletteFocusReturn(open: boolean): (event: Event) => void {
  const origin = useRef<{ element: HTMLElement; tabId: string | null } | null>(null);

  // Layout effect: capture before the palette's own input steals focus (next frame).
  useLayoutEffect(() => {
    if (!open) return;
    const el = document.activeElement;
    origin.current =
      el instanceof HTMLElement && el !== document.body
        ? { element: el, tabId: useTabStore.getState().activeTabId }
        : null;
  }, [open]);

  return useCallback((event: Event) => {
    event.preventDefault();
    const saved = origin.current;
    origin.current = null;
    if (!saved || !saved.element.isConnected) return;
    if (useTabStore.getState().activeTabId !== saved.tabId) return;
    const current = document.activeElement;
    if (current && current !== document.body) return;
    saved.element.focus();
  }, []);
}
