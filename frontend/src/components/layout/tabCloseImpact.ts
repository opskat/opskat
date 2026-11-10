import { registerTabCloseImpact } from "@/stores/tabStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useRDPStore } from "@/stores/rdpStore";
import { useVNCStore } from "@/stores/vncStore";
import { useSFTPStore } from "@/stores/sftpStore";
import { useOssTransferStore } from "@/stores/ossTransferStore";

/** 关闭 tab 的损失探针：把终端 / RDP / VNC 会话与 SFTP / OSS 在途传输接到 tabStore 的关闭确认上。 */
registerTabCloseImpact((tab) => {
  let sessions = 0;
  if (tab.meta.type === "terminal") {
    const data = useTerminalStore.getState().tabData[tab.id];
    sessions = data ? Object.values(data.panes).filter((p) => p.connected).length : 0;
  } else if (useVNCStore.getState().connectedTabIds.has(tab.id)) {
    sessions = 1;
  } else if (tab.meta.type === "page" && tab.meta.assetId != null) {
    sessions = useRDPStore.getState().activeAssetIds.has(tab.meta.assetId) ? 1 : 0;
  }
  const sftp = useSFTPStore
    .getState()
    .getTabTransfers(tab.id)
    .filter((t) => t.status === "active").length;
  const oss = Object.values(useOssTransferStore.getState().tabs[tab.id]?.transfers ?? {}).filter(
    (t) => t.status === "active"
  ).length;
  return { sessions, transfers: sftp + oss };
});
