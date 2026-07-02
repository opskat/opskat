import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Columns2, Rows2, RotateCcw, Pin, PinOff, X } from "lucide-react";
import { Button, cn } from "@opskat/ui";
import { useTerminalStore, TRANSPORTS } from "@/stores/terminalStore";
import { useTabStore, type TerminalTabMeta } from "@/stores/tabStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";

interface SessionToolbarProps {
  tabId: string;
  /** The pane this toolbar belongs to. Each split pane renders its own toolbar. */
  sessionId: string;
}

function formatUptime(connectedAt: number): string {
  const secs = Math.floor((Date.now() - connectedAt) / 1000);
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = secs % 60;
  return h > 0
    ? `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`
    : `${m}:${String(s).padStart(2, "0")}`;
}

function useUptime(connectedAt: number | undefined, connected: boolean, active: boolean): string {
  const [elapsedState, setElapsedState] = useState<{ connectedAt: number | undefined; elapsed: string }>({
    connectedAt,
    elapsed: connectedAt ? formatUptime(connectedAt) : "",
  });
  const elapsed =
    connected && connectedAt
      ? elapsedState.connectedAt === connectedAt
        ? elapsedState.elapsed
        : formatUptime(connectedAt)
      : "";

  useEffect(() => {
    if (!connected || !connectedAt) {
      return;
    }
    const update = () => {
      setElapsedState({ connectedAt, elapsed: formatUptime(connectedAt) });
    };
    update();
    if (!active) return; // 非活跃 tab 不启动 interval，切回时 effect 会重入并 update 一次
    const id = setInterval(update, 1000);
    return () => clearInterval(id);
  }, [connectedAt, connected, active]);
  return elapsed;
}

export function SessionToolbar({ tabId, sessionId }: SessionToolbarProps) {
  const { t } = useTranslation();
  const tabData = useTerminalStore((s) => s.tabData[tabId]);
  const splitPane = useTerminalStore((s) => s.splitPane);
  const reconnect = useTerminalStore((s) => s.reconnect);
  const closePane = useTerminalStore((s) => s.closePane);
  const setActivePaneId = useTerminalStore((s) => s.setActivePaneId);
  const isTabActive = useTabStore((s) => s.activeTabId === tabId);
  const pinned = useTerminalThemeStore((s) => s.toolbarPinned);
  const setToolbarPinned = useTerminalThemeStore((s) => s.setToolbarPinned);

  // hooks 必须在所有条件分支之前调用
  const pane = tabData?.panes[sessionId];
  const paneConnected = pane?.connected ?? false;
  const uptime = useUptime(pane?.connectedAt, paneConnected, isTabActive);

  const tabMeta = useTabStore((s) => {
    const t = s.tabs.find((t) => t.id === tabId);
    return t?.meta as TerminalTabMeta | undefined;
  });

  if (!tabData || !pane) return null;

  const paneCount = Object.keys(tabData.panes).length;
  const isSplit = paneCount > 1;
  const isActivePane = tabData.activePaneId === sessionId;
  // 仅当前 pane 的 transport 支持分屏才点亮分屏按钮(与右键菜单一致),
  // 否则像 serial 这种不可分屏的会出现"可点却无反应"的死按钮。
  const canSplit = TRANSPORTS[pane.transport ?? "ssh"].canSplit;

  const hostInfo =
    tabMeta?.username && tabMeta?.host
      ? `${tabMeta.username}@${tabMeta.host}${tabMeta.port !== 22 ? `:${tabMeta.port}` : ""}`
      : tabMeta?.host
        ? `${tabMeta.host}${tabMeta.port !== 22 ? `:${tabMeta.port}` : ""}`
        : "";

  // 拆分 / 重连作用于本窗格：先把它设为活动窗格，再执行 store 的活动窗格级操作。
  const focusPane = () => setActivePaneId(tabId, sessionId);

  return (
    <div
      className={cn(
        "flex items-center gap-1.5 px-2 py-1 border-b shrink-0 text-xs",
        // 分屏时非活动窗格的工具条淡化，帮助区分当前活动窗格；单窗格不淡化。
        isSplit && !isActivePane ? "bg-muted/40" : "bg-background"
      )}
    >
      {/* 连接状态指示器 */}
      <span
        className={`h-2 w-2 rounded-full shrink-0 ${paneConnected ? "bg-success" : "bg-destructive"}`}
        title={paneConnected ? t("ssh.session.connected") : t("ssh.session.disconnected")}
      />

      {/* 主机信息 */}
      {hostInfo && <span className="font-mono text-muted-foreground select-text truncate max-w-48">{hostInfo}</span>}

      {/* 连接时长 */}
      {uptime && (
        <>
          <span className="text-muted-foreground/40">|</span>
          <span className="font-mono text-muted-foreground tabular-nums">{uptime}</span>
        </>
      )}

      <div className="flex-1" />

      {/* 固定 / 隐藏切换（全局，持久化）：固定时点击切到自动隐藏，反之亦然。 */}
      <Button
        variant="ghost"
        size="icon-xs"
        title={pinned ? t("ssh.session.autoHideToolbar") : t("ssh.session.pinToolbar")}
        aria-label={pinned ? t("ssh.session.autoHideToolbar") : t("ssh.session.pinToolbar")}
        onClick={() => setToolbarPinned(!pinned)}
      >
        {pinned ? <Pin className="h-3.5 w-3.5" /> : <PinOff className="h-3.5 w-3.5" />}
      </Button>

      {/* 分割窗格按钮 */}
      <Button
        variant="ghost"
        size="icon-xs"
        title={t("ssh.session.splitH")}
        disabled={!paneConnected || !canSplit}
        onClick={() => {
          focusPane();
          splitPane(tabId, "horizontal");
        }}
      >
        <Rows2 className="h-3.5 w-3.5" />
      </Button>
      <Button
        variant="ghost"
        size="icon-xs"
        title={t("ssh.session.splitV")}
        disabled={!paneConnected || !canSplit}
        onClick={() => {
          focusPane();
          splitPane(tabId, "vertical");
        }}
      >
        <Columns2 className="h-3.5 w-3.5" />
      </Button>

      {/* 重新连接 */}
      <Button
        variant="ghost"
        size="icon-xs"
        title={t("ssh.session.reconnect")}
        onClick={() => {
          focusPane();
          reconnect(tabId);
        }}
      >
        <RotateCcw className="h-3.5 w-3.5" />
      </Button>

      {/* 关闭窗格：仅在分屏时出现——单窗格用关闭标签页即可，这里补上「选中即复制/右键粘贴」下丢失的关闭入口。 */}
      {isSplit && (
        <Button
          variant="ghost"
          size="icon-xs"
          title={t("ssh.contextMenu.closePane")}
          aria-label={t("ssh.contextMenu.closePane")}
          onClick={() => closePane(tabId, sessionId)}
        >
          <X className="h-3.5 w-3.5" />
        </Button>
      )}
    </div>
  );
}
