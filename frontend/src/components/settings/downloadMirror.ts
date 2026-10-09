import { useSettingsUiStore } from "@/stores/settingsUiStore";

/** DOM id of the app's "Download mirror" row in Settings → About (UpdateSection). */
export const DOWNLOAD_MIRROR_SETTING_ID = "settings-download-mirror";

/**
 * DOM id of the "Extension downloads" (registry mirror) card in Settings →
 * Extensions (ExtensionMirrorSettings), where store installs pull packages from.
 */
export const EXTENSION_MIRROR_SETTING_ID = "settings-extension-mirror";

/**
 * Opens Settings → About and brings the "Download mirror" setting into view with
 * its picker focused. The extension index is fetched through that mirror, so this
 * is where "Change mirror" goes when the index fails to load or verify.
 */
export function revealDownloadMirrorSetting() {
  useSettingsUiStore.getState().setActiveTab("about");
  revealSettingRow(DOWNLOAD_MIRROR_SETTING_ID);
}

/**
 * Brings the setting row id into view with its picker focused, on the next frame:
 * call it right after switching to the tab / view that mounts the row.
 */
export function revealSettingRow(id: string) {
  requestAnimationFrame(() => {
    const row = document.getElementById(id);
    row?.scrollIntoView({ block: "center", behavior: "smooth" });
    row?.querySelector<HTMLElement>("button")?.focus({ preventScroll: true });
  });
}
