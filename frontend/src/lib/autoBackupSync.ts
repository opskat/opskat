import { RequestAutoBackupCheck, SetAutoBackupClientSnapshot } from "../../wailsjs/go/system/System";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { serializeShortcutsForBackup, useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { useAssetStore } from "@/stores/assetStore";

/**
 * 自动备份需要的前端侧数据同步（docs/specs/2026-11-17-auto-backup-targets.md「检查与写入流程」）：
 * - 快捷键自定义项（含终端鼠标快捷键）、自定义终端主题只存在前端本地存储，变化时推给后端；后端收到第一份之后才开始检查。
 * - 界面上改了资产、分组、凭据、策略组、端口转发后请求一次检查（后端 2 秒防抖）；
 *   内置 AI / opsctl 的改动经 data:changed 事件同样请求检查，其余由定时检查兜底。
 */

function shortcutsSnapshot(): string {
  const { shortcuts, mouseShortcuts } = useShortcutStore.getState();
  return serializeShortcutsForBackup(shortcuts, mouseShortcuts) ?? "";
}

function themesSnapshot(): string {
  const themes = useTerminalThemeStore.getState().customThemes;
  return themes.length > 0 ? JSON.stringify(themes) : "";
}

function pushSnapshot() {
  SetAutoBackupClientSnapshot(shortcutsSnapshot(), themesSnapshot()).catch((e) =>
    console.error("sync auto backup snapshot failed:", e)
  );
}

/** 界面改了需要备份的数据后调用。 */
export function requestAutoBackupCheck() {
  RequestAutoBackupCheck().catch((e) => console.error("request auto backup check failed:", e));
}

/** 在 App 挂载时安装，返回卸载函数。 */
export function installAutoBackupSync(): () => void {
  pushSnapshot();
  const unsubShortcuts = useShortcutStore.subscribe((state, prev) => {
    if (state.shortcuts !== prev.shortcuts || state.mouseShortcuts !== prev.mouseShortcuts) pushSnapshot();
  });
  const unsubThemes = useTerminalThemeStore.subscribe((state, prev) => {
    if (state.customThemes !== prev.customThemes) pushSnapshot();
  });
  const unsubAssets = useAssetStore.subscribe((state, prev) => {
    if (state.assets !== prev.assets || state.groups !== prev.groups) requestAutoBackupCheck();
  });
  const cancelDataChanged = EventsOn("data:changed", requestAutoBackupCheck);
  return () => {
    unsubShortcuts();
    unsubThemes();
    unsubAssets();
    cancelDataChanged();
  };
}
