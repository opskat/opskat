import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { cn, Button, Switch, Label } from "@opskat/ui";
import { RotateCcw } from "lucide-react";
import {
  useShortcutStore,
  SHORTCUT_ACTIONS,
  DEFAULT_SHORTCUTS,
  formatBinding,
  findShortcutConflict,
  isMac,
  type ShortcutAction,
  type ShortcutBinding,
} from "@/stores/shortcutStore";

export function ShortcutSettings() {
  const { t } = useTranslation();
  const {
    shortcuts,
    disableShortcutsInTerminal,
    setDisableShortcutsInTerminal,
    updateShortcut,
    resetShortcut,
    resetAll,
    swapCmdCtrl,
    setIsRecording,
  } = useShortcutStore();
  const [recording, setRecording] = useState<ShortcutAction | null>(null);
  const [conflict, setConflict] = useState<{ action: ShortcutAction; conflictAction: ShortcutAction } | null>(null);

  const startRecording = (action: ShortcutAction) => {
    setConflict(null);
    setRecording(action);
    setIsRecording(true);
  };

  const stopRecording = useCallback(() => {
    setRecording(null);
    setIsRecording(false);
  }, [setIsRecording]);

  const handleRecord = useCallback(
    (e: KeyboardEvent) => {
      if (!recording) return;

      if (e.key === "Escape") {
        stopRecording();
        return;
      }

      // Ignore modifier-only presses
      if (["Meta", "Control", "Shift", "Alt"].includes(e.key)) return;

      e.preventDefault();
      e.stopPropagation();

      const binding: ShortcutBinding = {
        code: e.code,
        mod: isMac ? e.metaKey : e.ctrlKey,
        ctrl: isMac ? e.ctrlKey : false,
        shift: e.shiftKey,
        alt: e.altKey,
      };

      const conflictAction = findShortcutConflict(recording, binding, useShortcutStore.getState().shortcuts);
      if (conflictAction) {
        setConflict({ action: recording, conflictAction });
        stopRecording();
        return;
      }

      setConflict(null);
      updateShortcut(recording, binding);
      stopRecording();
    },
    [recording, updateShortcut, stopRecording]
  );

  useEffect(() => {
    if (!recording) return;
    window.addEventListener("keydown", handleRecord, true);
    return () => window.removeEventListener("keydown", handleRecord, true);
  }, [recording, handleRecord]);

  // Cancel recording on click outside
  useEffect(() => {
    if (!recording) return;
    const cancel = () => stopRecording();
    // Delay to avoid the click that started recording
    const timer = setTimeout(() => {
      window.addEventListener("mousedown", cancel);
    }, 100);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("mousedown", cancel);
    };
  }, [recording, stopRecording]);

  const isCustomized = (action: ShortcutAction) => {
    const def = DEFAULT_SHORTCUTS[action];
    const cur = shortcuts[action];
    return (
      cur.code !== def.code ||
      cur.mod !== def.mod ||
      cur.ctrl !== def.ctrl ||
      cur.shift !== def.shift ||
      cur.alt !== def.alt
    );
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between py-3 px-2 rounded border bg-muted/30">
        <div className="space-y-0.5">
          <Label htmlFor="disable-shortcuts-in-terminal" className="text-sm font-medium">
            {t("shortcut.disableInTerminalTitle")}
          </Label>
          <p className="text-xs text-muted-foreground">{t("shortcut.disableInTerminalDesc")}</p>
        </div>
        <Switch
          id="disable-shortcuts-in-terminal"
          checked={disableShortcutsInTerminal}
          onCheckedChange={setDisableShortcutsInTerminal}
        />
      </div>
      <div className="space-y-0.5">
        {SHORTCUT_ACTIONS.map((action) => (
          <div key={action} className="flex items-center justify-between py-2 px-2 rounded hover:bg-muted/50">
            <div className="grid gap-0.5">
              <span className="text-sm">{t(`shortcut.${action}`)}</span>
              {conflict?.action === action && (
                <span className="text-xs text-destructive">
                  {t("shortcut.conflict", { action: t(`shortcut.${conflict.conflictAction}`) })}
                </span>
              )}
            </div>
            <div className="flex items-center gap-1">
              <button
                className={cn(
                  "px-2.5 py-1 rounded text-xs font-mono min-w-[80px] text-center transition-colors",
                  recording === action
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted hover:bg-muted/80 cursor-pointer"
                )}
                onClick={(e) => {
                  e.stopPropagation();
                  startRecording(action);
                }}
              >
                {recording === action ? t("shortcut.recording") : formatBinding(shortcuts[action])}
              </button>
              {isCustomized(action) && (
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6"
                  onClick={() => {
                    setConflict(null);
                    resetShortcut(action);
                  }}
                  title={t("shortcut.reset")}
                >
                  <RotateCcw className="h-3 w-3" />
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setConflict(null);
            resetAll();
          }}
        >
          {t("shortcut.resetAll")}
        </Button>
        {isMac && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setConflict(null);
              swapCmdCtrl();
            }}
            title={t("shortcut.swapCmdCtrlDesc")}
          >
            {t("shortcut.swapCmdCtrl")}
          </Button>
        )}
      </div>
    </div>
  );
}
