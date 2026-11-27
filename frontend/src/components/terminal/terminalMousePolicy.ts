import {
  terminalMouseButtonFromCode,
  type TerminalMouseAction,
  type TerminalMouseBindings,
  type TerminalSelectionAction,
} from "@/stores/terminalMouseShortcuts";

export type MouseTrackingMode = "none" | "x10" | "vt200" | "drag" | "any";

export const MOUSE_DRAG_THRESHOLD_PX = 5;

export type ExecutableTerminalMouseAction = Exclude<
  TerminalMouseAction,
  "none" | "terminal.contextMenu" | "terminal.copyOrPaste"
>;

export type ResolvedMouseOutcome =
  | { kind: "passthrough" }
  | { kind: "suppress" }
  | { kind: "contextMenu" }
  | { kind: "action"; action: ExecutableTerminalMouseAction };

export interface MouseEventSnapshot {
  button: number;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
}

export interface MousePolicyContext {
  bindings: TerminalMouseBindings;
  mouseTrackingMode: MouseTrackingMode;
  hasSelection: boolean;
  readOnly: boolean;
  wasDragged?: boolean;
}

const READ_ONLY_FORBIDDEN_ACTIONS = new Set<TerminalMouseAction>(["terminal.paste", "terminal.clear"]);

export function isMouseDrag(startX: number, startY: number, endX: number, endY: number): boolean {
  return Math.hypot(endX - startX, endY - startY) > MOUSE_DRAG_THRESHOLD_PX;
}

export function isMacCtrlLeftContextMenu(
  event: Pick<MouseEventSnapshot, "ctrlKey">,
  lastMouseDownButton: number | null,
  isMac: boolean
): boolean {
  return isMac && event.ctrlKey && lastMouseDownButton === 0;
}

export function shouldAutoCopySelection(
  selection: string | undefined,
  selectionAction: TerminalSelectionAction,
  lastCopied: string
): string | null {
  if (selectionAction !== "auto-copy" || !selection) return null;
  if (!selection.trim()) return null;
  if (selection === lastCopied) return null;
  return selection;
}

function resolveAction(
  action: TerminalMouseAction,
  isRightButton: boolean,
  hasSelection: boolean,
  readOnly: boolean
): ResolvedMouseOutcome {
  if (action === "none") {
    return isRightButton ? { kind: "suppress" } : { kind: "passthrough" };
  }
  if (action === "terminal.contextMenu") {
    return { kind: "contextMenu" };
  }
  if (action === "terminal.copyOrPaste") {
    if (hasSelection) return { kind: "action", action: "terminal.copy" };
    if (readOnly) return isRightButton ? { kind: "suppress" } : { kind: "passthrough" };
    return { kind: "action", action: "terminal.paste" };
  }
  if (readOnly && READ_ONLY_FORBIDDEN_ACTIONS.has(action)) {
    return isRightButton ? { kind: "suppress" } : { kind: "passthrough" };
  }
  return { kind: "action", action };
}

/**
 * Pure decision function for fixed 4-button terminal mouse interactions (Right, Middle, Back, Forward).
 *
 * Covers:
 * - Drag guard (> 5px movement suppresses click actions / context menu)
 * - Remote TUI mouse tracking (`vim`/`tmux`/`htop`): passes Middle/Right through without Shift,
 *   restores OpsKat behavior when Shift is held, while Side buttons (Back/Forward) always stay active
 * - Right-click escape hatch: `Shift + Right Click` always opens context menu even when bare Right Click is rebound
 * - Read-only terminals (`K8sLogTerminal`): skips write actions (`terminal.paste`, `terminal.clear`)
 */
export function resolveTerminalMousePolicy(event: MouseEventSnapshot, ctx: MousePolicyContext): ResolvedMouseOutcome {
  const button = terminalMouseButtonFromCode(event.button);
  if (!button) return { kind: "passthrough" };

  const isRight = button === "right";
  const isSide = button === "back" || button === "forward";

  if (ctx.wasDragged) {
    return isRight ? { kind: "suppress" } : { kind: "passthrough" };
  }

  const tuiActive = ctx.mouseTrackingMode !== "none";
  const hasOtherModifier = event.metaKey || event.ctrlKey || event.altKey;

  // Remote TUI captures Middle/Right unless Shift bypasses it.
  // Side buttons (Mouse4/Mouse5) are outside VT200/SGR mouse protocols and stay active.
  if (tuiActive && !isSide && !event.shiftKey) {
    return isRight ? { kind: "suppress" } : { kind: "passthrough" };
  }

  if (hasOtherModifier) {
    return isRight ? { kind: "contextMenu" } : { kind: "passthrough" };
  }

  if (isRight) {
    // Shift+Right is both the TUI bypass and the context-menu escape hatch when bare Right Click is rebound.
    if (event.shiftKey) return { kind: "contextMenu" };
    return resolveAction(ctx.bindings.right, true, ctx.hasSelection, ctx.readOnly);
  }

  if (isSide) {
    if (event.shiftKey) return { kind: "passthrough" };
    return resolveAction(ctx.bindings[button], false, ctx.hasSelection, ctx.readOnly);
  }

  // Middle click (bare in normal mode, or Shift+Middle to bypass TUI / consistent tmux muscle memory).
  return resolveAction(ctx.bindings.middle, false, ctx.hasSelection, ctx.readOnly);
}

/**
 * Decide whether `mousedown` capture must call `event.preventDefault()`.
 * - Middle click (button 1): prevents Windows WebView2 autoscroll wheel and Linux X11 double-paste
 *   when Middle click resolves to an active OpsKat action/menu.
 * - Side buttons (button 3/4): always prevent WebView2/WebKit browser history navigation inside terminals.
 * - Left (0) & Right (2): never prevented on mousedown (Left needs focus/selection; Right uses contextmenu).
 */
export function shouldPreventMouseDownDefault(event: MouseEventSnapshot, ctx: MousePolicyContext): boolean {
  if (event.button === 3 || event.button === 4) {
    return true;
  }
  if (event.button === 1) {
    const outcome = resolveTerminalMousePolicy(event, { ...ctx, wasDragged: false });
    return outcome.kind === "action" || outcome.kind === "contextMenu";
  }
  return false;
}
