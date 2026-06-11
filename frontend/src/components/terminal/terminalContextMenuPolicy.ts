import type { TerminalCopyBehavior } from "@/stores/terminalThemeStore";

export type TerminalContextMenuAction = "menu" | "copy" | "paste";

export function getTerminalContextMenuAction(
  behavior: TerminalCopyBehavior,
  selection: string | undefined
): TerminalContextMenuAction {
  if (behavior === "popover-menu") return "menu";
  if (behavior === "select-copy-right-paste") return "paste";
  return selection ? "copy" : "paste";
}
