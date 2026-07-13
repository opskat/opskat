import { create } from "zustand";
import { toast } from "sonner";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import {
  OSSUploadObject,
  OSSUploadObjectPath,
  OSSDownloadObject,
  OSSStartTransfer,
  OSSCancelTransfer,
} from "../../wailsjs/go/oss/OSS";
import { registerTabCloseHook, type QueryTabMeta } from "./tabStore";
import { useOssBrowserStore } from "./ossBrowserStore";
import i18n from "../i18n";

const DONE_LINGER_MS = 5000;

export type OssTransferStatus = "active" | "done" | "error" | "cancelled";

export interface OssTransfer {
  transferId: string;
  tabId: string;
  direction: "upload" | "download";
  name: string;
  targetPrefix?: string;
  bytesDone: number;
  bytesTotal: number;
  speed: number;
  status: OssTransferStatus;
  error?: string;
}

interface OssTransferProgressEvent {
  transferId: string;
  status: "progress" | "done" | "error" | "cancelled";
  currentFile: string;
  filesCompleted: number;
  filesTotal: number;
  bytesDone: number;
  bytesTotal: number;
  speed: number;
  error?: string;
}

interface OssTransferTabState {
  transfers: Record<string, OssTransfer>;
}

interface OssTransferState {
  tabs: Record<string, OssTransferTabState>;
  startUpload: (tabId: string, assetId: number, bucket: string, prefix: string) => Promise<void>;
  startUploadPath: (tabId: string, assetId: number, bucket: string, prefix: string, localPath: string) => Promise<void>;
  startDownload: (tabId: string, assetId: number, bucket: string, key: string) => Promise<void>;
  cancel: (transferId: string) => void;
  clear: (tabId: string, transferId: string) => void;
  clearCompleted: (tabId: string) => void;
}

/** 取路径末段(兼容 / 和 \,去掉结尾分隔符)。 */
function basename(p: string): string {
  const trimmed = p.replace(/[/\\]+$/, "");
  const i = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return i >= 0 ? trimmed.slice(i + 1) : trimmed;
}

export const useOssTransferStore = create<OssTransferState>((set, get) => {
  const addTransfer = (t: OssTransfer) =>
    set((s) => {
      const tab = s.tabs[t.tabId] ?? { transfers: {} };
      return { tabs: { ...s.tabs, [t.tabId]: { transfers: { ...tab.transfers, [t.transferId]: t } } } };
    });

  // 只对已存在的 tab+transfer 打补丁(tab 关闭 / 已被清理后不重建)。
  const patchTransfer = (tabId: string, transferId: string, fn: (t: OssTransfer) => OssTransfer) =>
    set((s) => {
      const tab = s.tabs[tabId];
      if (!tab || !tab.transfers[transferId]) return {};
      return {
        tabs: { ...s.tabs, [tabId]: { transfers: { ...tab.transfers, [transferId]: fn(tab.transfers[transferId]) } } },
      };
    });

  const removeTransfer = (tabId: string, transferId: string) =>
    set((s) => {
      const tab = s.tabs[tabId];
      if (!tab) return {};
      const transfers = { ...tab.transfers };
      delete transfers[transferId];
      return { tabs: { ...s.tabs, [tabId]: { transfers } } };
    });

  // 上传完成后:若目标前缀 === 当前浏览前缀,则刷新对象列表(store→store,via getState)。
  const maybeRefreshAfterUpload = (tabId: string, targetPrefix?: string) => {
    const browser = useOssBrowserStore.getState();
    const current = browser.tabs[tabId]?.currentPrefix;
    if (targetPrefix === undefined || current === undefined || targetPrefix !== current) return;
    void browser.refresh(tabId).catch(() => {
      toast.error(i18n.t("oss.transfer.refreshAfterUploadFailed"));
    });
  };

  const subscribeProgress = (tabId: string, transferId: string) => {
    const eventName = "transfer:progress:" + transferId;
    EventsOn(eventName, (e: OssTransferProgressEvent) => {
      if (!get().tabs[tabId]?.transfers[transferId]) return;
      switch (e.status) {
        case "progress":
          patchTransfer(tabId, transferId, (t) => ({
            ...t,
            name: e.currentFile ? basename(e.currentFile) : t.name,
            bytesDone: e.bytesDone,
            bytesTotal: e.bytesTotal,
            speed: e.speed,
          }));
          break;
        case "done": {
          patchTransfer(tabId, transferId, (t) => ({ ...t, status: "done", bytesDone: t.bytesTotal || t.bytesDone }));
          EventsOff(eventName);
          const done = get().tabs[tabId]?.transfers[transferId];
          if (done?.direction === "upload") maybeRefreshAfterUpload(tabId, done.targetPrefix);
          setTimeout(() => {
            if (get().tabs[tabId]?.transfers[transferId]?.status === "done") removeTransfer(tabId, transferId);
          }, DONE_LINGER_MS);
          break;
        }
        case "cancelled":
          // OSS 显式发 "cancelled"(不像 SFTP 从错误子串推断)。
          patchTransfer(tabId, transferId, (t) => ({ ...t, status: "cancelled" }));
          EventsOff(eventName);
          break;
        case "error":
          patchTransfer(tabId, transferId, (t) => ({ ...t, status: "error", error: e.error }));
          EventsOff(eventName);
          break;
      }
    });
  };

  return {
    tabs: {},

    startUpload: async (tabId, assetId, bucket, prefix) => {
      const ids = await OSSUploadObject(assetId, bucket, prefix); // 空数组 = 用户取消对话框
      for (const id of ids) {
        addTransfer({
          transferId: id,
          tabId,
          direction: "upload",
          name: "",
          targetPrefix: prefix,
          bytesDone: 0,
          bytesTotal: 0,
          speed: 0,
          status: "active",
        });
        subscribeProgress(tabId, id);
        await OSSStartTransfer(id);
      }
    },

    startUploadPath: async (tabId, assetId, bucket, prefix, localPath) => {
      const name = basename(localPath);
      const id = await OSSUploadObjectPath(assetId, bucket, prefix + name, localPath);
      if (!id) return;
      addTransfer({
        transferId: id,
        tabId,
        direction: "upload",
        name,
        targetPrefix: prefix,
        bytesDone: 0,
        bytesTotal: 0,
        speed: 0,
        status: "active",
      });
      subscribeProgress(tabId, id);
      await OSSStartTransfer(id);
    },

    startDownload: async (tabId, assetId, bucket, key) => {
      const id = await OSSDownloadObject(assetId, bucket, key);
      if (!id) return; // 空串 = 用户取消保存对话框
      addTransfer({
        transferId: id,
        tabId,
        direction: "download",
        name: basename(key),
        bytesDone: 0,
        bytesTotal: 0,
        speed: 0,
        status: "active",
      });
      subscribeProgress(tabId, id);
      await OSSStartTransfer(id);
    },

    cancel: (transferId) => {
      void OSSCancelTransfer(transferId);
    },

    clear: (tabId, transferId) => removeTransfer(tabId, transferId),

    clearCompleted: (tabId) =>
      set((s) => {
        const tab = s.tabs[tabId];
        if (!tab) return {};
        const transfers: Record<string, OssTransfer> = {};
        for (const [id, t] of Object.entries(tab.transfers)) {
          if (t.status === "active") transfers[id] = t;
        }
        return { tabs: { ...s.tabs, [tabId]: { transfers } } };
      }),
  };
});

// tab 关闭时退订所有事件并清理该 OSS query tab 的传输态。
registerTabCloseHook((tab) => {
  if (tab.type !== "query") return;
  if ((tab.meta as QueryTabMeta).assetType !== "oss") return;
  useOssTransferStore.setState((s) => {
    const tabState = s.tabs[tab.id];
    if (tabState) {
      for (const id of Object.keys(tabState.transfers)) EventsOff("transfer:progress:" + id);
    }
    const next = { ...s.tabs };
    delete next[tab.id];
    return { tabs: next };
  });
});
