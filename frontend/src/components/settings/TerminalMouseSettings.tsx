import { useCallback, useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Label,
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
  Separator,
} from "@opskat/ui";
import { RotateCcw } from "lucide-react";
import { useShortcutStore } from "@/stores/shortcutStore";
import {
  DEFAULT_TERMINAL_MOUSE_BINDINGS,
  mouseShortcutsEqual,
  TERMINAL_MOUSE_BUTTONS,
  TERMINAL_MOUSE_TERMINAL_ACTIONS,
  TERMINAL_MOUSE_WORKSPACE_ACTIONS,
  type TerminalMouseAction,
  type TerminalSelectionAction,
} from "@/stores/terminalMouseShortcuts";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";

const SELECTION_ACTION_HINT_KEYS: Record<TerminalSelectionAction, string> = {
  popover: "terminal.mouse.selectionActionPopoverHint",
  "auto-copy": "terminal.mouse.selectionActionAutoCopyHint",
  none: "terminal.mouse.selectionActionNoneHint",
};

export function TerminalMouseSettings() {
  const { t } = useTranslation();
  const cardRef = useRef<HTMLDivElement>(null);

  const selectionAction = useTerminalThemeStore((s) => s.selectionAction);
  const setSelectionAction = useTerminalThemeStore((s) => s.setSelectionAction);

  const mouseShortcuts = useShortcutStore((s) => s.mouseShortcuts);
  const updateMouseShortcut = useShortcutStore((s) => s.updateMouseShortcut);
  const resetMouseShortcut = useShortcutStore((s) => s.resetMouseShortcut);
  const resetAllMouseShortcuts = useShortcutStore((s) => s.resetAllMouseShortcuts);

  const focusSection = useSettingsUiStore((s) => s.focusSection);
  const clearFocusSection = useSettingsUiStore((s) => s.clearFocusSection);

  useEffect(() => {
    if (focusSection !== "mouse-selection") return;
    const el = cardRef.current;
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ behavior: "smooth", block: "start" });
    }
    clearFocusSection();
  }, [focusSection, clearFocusSection]);

  const actionLabel = useCallback((action: TerminalMouseAction) => t(`terminal.mouse.action.${action}`), [t]);

  const isAnyCustomized = useMemo(
    () => !mouseShortcutsEqual(mouseShortcuts, DEFAULT_TERMINAL_MOUSE_BINDINGS),
    [mouseShortcuts]
  );

  return (
    <Card ref={cardRef} data-testid="terminal-mouse-selection-card">
      <CardHeader>
        <CardTitle className="text-base">{t("terminal.mouse.title")}</CardTitle>
        <CardDescription>{t("terminal.mouse.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* After selecting text */}
        <div className="grid gap-2">
          <Label htmlFor="terminal-selection-action">{t("terminal.mouse.selectionAction")}</Label>
          <Select
            value={selectionAction}
            onValueChange={(value) => setSelectionAction(value as TerminalSelectionAction)}
          >
            <SelectTrigger id="terminal-selection-action" data-testid="terminal-selection-action" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="popover">{t("terminal.mouse.selectionActionPopover")}</SelectItem>
              <SelectItem value="auto-copy">{t("terminal.mouse.selectionActionAutoCopy")}</SelectItem>
              <SelectItem value="none">{t("terminal.mouse.selectionActionNone")}</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground" data-testid="terminal-selection-action-hint">
            {t(SELECTION_ACTION_HINT_KEYS[selectionAction])}
          </p>
        </div>

        <Separator />

        {/* Fixed 4-button terminal mouse shortcuts */}
        <div className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="space-y-0.5">
              <Label className="text-sm font-medium">{t("terminal.mouse.shortcutsTitle")}</Label>
              <p className="text-xs text-muted-foreground">{t("terminal.mouse.shortcutsHint")}</p>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              data-testid="reset-mouse-shortcuts-btn"
              disabled={!isAnyCustomized}
              onClick={resetAllMouseShortcuts}
            >
              {t("shortcut.resetAll")}
            </Button>
          </div>

          <div className="space-y-1.5">
            {TERMINAL_MOUSE_BUTTONS.map((button) => {
              const action = mouseShortcuts[button];
              const isSlotCustomized = action !== DEFAULT_TERMINAL_MOUSE_BINDINGS[button];
              return (
                <div
                  key={button}
                  data-testid={`mouse-shortcut-row-${button}`}
                  className="flex items-center justify-between gap-3 rounded-md border bg-muted/20 px-3 py-2"
                >
                  <span className="text-sm font-medium">{t(`terminal.mouse.button.${button}`)}</span>

                  <div className="flex items-center gap-1.5">
                    <Select
                      value={action}
                      onValueChange={(value) => updateMouseShortcut(button, value as TerminalMouseAction)}
                    >
                      <SelectTrigger
                        className="h-8 w-52 max-w-full text-xs"
                        data-testid={`mouse-shortcut-action-${button}`}
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="none">{actionLabel("none")}</SelectItem>
                        <SelectSeparator />
                        <SelectGroup>
                          <SelectLabel>{t("terminal.mouse.groupTerminal")}</SelectLabel>
                          {TERMINAL_MOUSE_TERMINAL_ACTIONS.map((item) => (
                            <SelectItem key={item} value={item}>
                              {actionLabel(item)}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                        <SelectSeparator />
                        <SelectGroup>
                          <SelectLabel>{t("terminal.mouse.groupWorkspace")}</SelectLabel>
                          {TERMINAL_MOUSE_WORKSPACE_ACTIONS.map((item) => (
                            <SelectItem key={item} value={item}>
                              {actionLabel(item)}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>

                    <div className="flex w-7 shrink-0 items-center justify-center">
                      {isSlotCustomized && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          data-testid={`mouse-shortcut-reset-${button}`}
                          title={t("shortcut.reset")}
                          aria-label={t("shortcut.reset")}
                          onClick={() => resetMouseShortcut(button)}
                        >
                          <RotateCcw className="h-3.5 w-3.5" />
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>

          <p className="text-xs text-muted-foreground">{t("terminal.mouse.tuiTrackingHint")}</p>
        </div>
      </CardContent>
    </Card>
  );
}
