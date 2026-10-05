import { useSettingsUiStore } from "@/stores/settingsUiStore";

/** DOM id of the app's "Download mirror" row in Settings → About (UpdateSection). */
export const DOWNLOAD_MIRROR_SETTING_ID = "settings-download-mirror";

/**
 * Opens Settings → About and brings the "Download mirror" setting into view with
 * its picker focused. The extension index is fetched through that mirror, so this
 * is where "Change mirror" goes when the index fails to load or verify.
 */
export function revealDownloadMirrorSetting() {
  useSettingsUiStore.getState().setActiveTab("about");
  // The About tab mounts on the next render; reach for the row after it.
  requestAnimationFrame(() => {
    const row = document.getElementById(DOWNLOAD_MIRROR_SETTING_ID);
    row?.scrollIntoView({ block: "center", behavior: "smooth" });
    row?.querySelector<HTMLElement>("button")?.focus({ preventScroll: true });
  });
}
