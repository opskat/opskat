import { SetWebDAVAutoBackupClientSnapshot } from "../../wailsjs/go/system/System";
import { useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";

const SHORTCUT_STORAGE_KEY = "keyboard_shortcuts";

function shortcutsSnapshot() {
  return localStorage.getItem(SHORTCUT_STORAGE_KEY) || "";
}

function customThemesSnapshot() {
  const themes = useTerminalThemeStore.getState().customThemes;
  return themes.length > 0 ? JSON.stringify(themes) : "";
}

let syncTimer: ReturnType<typeof setTimeout> | null = null;

function syncSnapshot() {
  if (syncTimer) clearTimeout(syncTimer);
  syncTimer = setTimeout(() => {
    syncTimer = null;
    SetWebDAVAutoBackupClientSnapshot(shortcutsSnapshot(), customThemesSnapshot()).catch(() => {});
  }, 250);
}

export function installWebDAVAutoBackupSnapshotSync() {
  syncSnapshot();
  const unsubscribeShortcuts = useShortcutStore.subscribe((state, prev) => {
    if (state.shortcuts !== prev.shortcuts) syncSnapshot();
  });
  const unsubscribeThemes = useTerminalThemeStore.subscribe((state, prev) => {
    if (state.customThemes !== prev.customThemes) syncSnapshot();
  });
  return () => {
    unsubscribeShortcuts();
    unsubscribeThemes();
    if (syncTimer) {
      clearTimeout(syncTimer);
      syncTimer = null;
    }
  };
}
