// frontend/src/extension/init.ts
import { toast } from "sonner";
import i18n from "../i18n";
import { ListInstalledExtensions } from "../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { useExtensionStore } from "./store";
import { injectExtensionAPI } from "./inject";
import { createExtensionAPI } from "./api";
import { clearExtensionCache } from "./loader";
import type { ExtManifest } from "./types";

let _bootstrapped = false;
let _subscribed = false;

/**
 * One-shot bootstrap: inject API, load extension list, subscribe to events.
 * If the backend hasn't finished init yet (returns empty), ready is NOT set —
 * we wait for the ext:ready event from the backend to refresh and set ready.
 * Safe to call multiple times — only the first call takes effect.
 */
export async function bootstrapExtensions(): Promise<void> {
  if (_bootstrapped) return;
  _bootstrapped = true;
  injectExtensionAPI(createExtensionAPI());
  subscribeExtensionReload(); // subscribe BEFORE async gap — no events lost
  subscribeExtensionReady();
  const loaded = await refreshExtensions();
  // 只有实际获取到扩展时才设置 ready，否则等 ext:ready 事件
  if (loaded) {
    useExtensionStore.getState().setReady(true);
  }
}

/**
 * Register ext:reload event listener. Returns cleanup function.
 * Safe to call multiple times — only the first call registers.
 */
export function subscribeExtensionReload(): () => void {
  if (_subscribed) return () => {};
  _subscribed = true;
  const cancel = EventsOn("ext:reload", () => {
    clearExtensionCache();
    refreshExtensions();
  });
  return cancel;
}

let _readySubscribed = false;

/**
 * Listen for ext:ready from backend (emitted after extension init completes).
 * On receive, refresh extensions and mark ready.
 */
function subscribeExtensionReady(): void {
  if (_readySubscribed) return;
  _readySubscribed = true;
  EventsOn("ext:ready", async () => {
    await refreshExtensions();
    useExtensionStore.getState().setReady(true);
  });
}

/**
 * Refresh extension list from the backend.
 * Returns true if extensions were loaded, false if the list was empty.
 */
async function refreshExtensions(): Promise<boolean> {
  try {
    const extensions = await ListInstalledExtensions();
    const store = useExtensionStore.getState();

    const list = extensions || [];
    const installed = new Set(list.map((e: { name: string }) => e.name));
    for (const name of new Set([...Object.keys(store.extensions), ...Object.keys(store.disabled)])) {
      if (!installed.has(name)) {
        store.unregister(name);
      }
    }

    // ListInstalled 连禁用的扩展也返回（Enabled=false）：禁用必须等同于"未注册"，
    // 否则它的资产类型仍可选、页面仍能打开，而后端调用全部失败。
    for (const ext of list) {
      if (ext.enabled) {
        store.register(ext.name, ext.manifest as ExtManifest);
      } else {
        store.markDisabled(ext.name);
      }
    }

    return list.length > 0;
  } catch (err) {
    toast.error(`${i18n.t("extension.loadError")}: ${String(err)}`);
    return false;
  }
}

// Exports for testing only
export { refreshExtensions as _refreshExtensions };
export function _resetForTesting(): void {
  _bootstrapped = false;
  _subscribed = false;
  _readySubscribed = false;
}
