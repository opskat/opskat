import { create } from "zustand";
import { ConnectSSHAsync } from "../../wailsjs/go/ssh/SSH";
import {
  CancelSSHConnect,
  RespondAuthChallenge,
  RespondHostKeyVerify,
  DisconnectSSH,
  GetSSHSyncState,
  ResizeSSH,
  SplitSSH,
  UpdateAssetPassword,
  WriteSSH,
} from "../../wailsjs/go/ssh/SSH";
import {
  WriteSerial,
  ConnectSerialAsync,
  DisconnectSerial,
  ResizeSerialTerminal,
} from "../../wailsjs/go/serial/Serial";
import {
  WriteLocal,
  ConnectLocalAsync,
  DisconnectLocal,
  ResizeLocalTerminal,
  SplitLocal,
} from "../../wailsjs/go/local/Local";
import { ssh as ssh_models, asset_entity } from "../../wailsjs/go/models";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { bytesToBase64 } from "../lib/terminalEncode";
import { errorMessage } from "../lib/errorMessage";
import { createOrderedQueue, type OrderedQueue } from "../lib/orderedQueue";
import { useTabStore, registerTabCloseHook, registerTabRestoreHook, type TerminalTabMeta } from "./tabStore";
import { useAssetStore } from "./assetStore";

function disposeTerminalInstance(sessionId: string): void {
  import("@/components/terminal/terminalRegistry")
    .then(({ disposeTerminal }) => disposeTerminal(sessionId))
    .catch((error) => console.error(`Failed to dispose terminal instance ${sessionId}:`, error));
}

export type TerminalTransport = "ssh" | "serial" | "local";

interface TransportSpec {
  connectAsync: (
    assetId: number,
    opts: { cols: number; rows: number; password: string; initialWorkdir?: string }
  ) => Promise<string>;
  write: (sessionId: string, dataB64: string) => Promise<void>;
  resize: (sessionId: string, cols: number, rows: number) => Promise<void>;
  disconnect: (sessionId: string) => void;
  eventPrefix: string;
  canSplit: boolean;
  /** 分屏:基于现有会话开一个新会话,返回新 sessionId。canSplit 为 true 时必须提供。 */
  split?: (existingSessionId: string, cols: number, rows: number) => Promise<string>;
  /** 仅 ssh 会同步 cwd / 暴露 SFTP 等目录能力。 */
  hasDirectorySync: boolean;
}

// 把「写入某会话」的后端调用按 sessionId 串行化：所有出站写(终端输入/粘贴、ZMODEM 出站
// 字节、rz 触发)都经 spec.write，统一在此保序，互不串扰。否则 Wails 的并发 IPC 派发会让
// 突发写乱序抵达后端，损坏 ZMODEM 传输与大段粘贴(详见 lib/orderedQueue.ts)。
export function orderedBySession(
  raw: (sessionId: string, dataB64: string) => Promise<void>
): (sessionId: string, dataB64: string) => Promise<void> {
  const queues = new Map<string, OrderedQueue>();
  return (sessionId, dataB64) => {
    let q = queues.get(sessionId);
    if (!q) {
      q = createOrderedQueue();
      queues.set(sessionId, q);
    }
    const result = q.push(() => raw(sessionId, dataB64));
    // 会话写空闲即回收 map 条目，避免随开过的会话无限增长。
    void result
      .catch(() => undefined)
      .then(() => {
        const cur = queues.get(sessionId);
        if (cur && cur.idle) queues.delete(sessionId);
      });
    return result;
  };
}

// 单一 transport 能力表：连接/读写/尺寸/断开/事件前缀/分屏/目录同步全部按 transport 查表，
// 取代散落各处的 isSerial 二分支。新增 transport 只需在此登记一行。
export const TRANSPORTS: Record<TerminalTransport, TransportSpec> = {
  ssh: {
    connectAsync: (assetId, { cols, rows, password, initialWorkdir }) =>
      ConnectSSHAsync(
        new ssh_models.SSHConnectRequest({
          assetId,
          password,
          key: "",
          cols,
          rows,
          initialWorkdir: initialWorkdir ?? "",
        })
      ),
    write: orderedBySession(WriteSSH),
    resize: ResizeSSH,
    disconnect: DisconnectSSH,
    eventPrefix: "ssh",
    canSplit: true,
    split: SplitSSH,
    hasDirectorySync: true,
  },
  serial: {
    connectAsync: (assetId) => ConnectSerialAsync({ assetId }),
    write: orderedBySession(WriteSerial),
    resize: ResizeSerialTerminal,
    disconnect: DisconnectSerial,
    eventPrefix: "serial",
    canSplit: false,
    hasDirectorySync: false,
  },
  local: {
    connectAsync: (assetId, { cols, rows }) => ConnectLocalAsync({ assetId, cols, rows }),
    write: orderedBySession(WriteLocal),
    resize: ResizeLocalTerminal,
    disconnect: DisconnectLocal,
    eventPrefix: "local",
    // 本地无连接可复用,分屏即再起一个同 shell 配置的 PTY(同 iTerm/tmux),由 SplitLocal 实现。
    canSplit: true,
    split: SplitLocal,
    hasDirectorySync: false,
  },
};

export function transportForAsset(assetType: string): TerminalTransport {
  if (assetType === "serial") return "serial";
  if (assetType === "local") return "local";
  return "ssh";
}

// 仅在恢复（restore）时用到——那时 pane 状态还没有 transport，只能从 session id 前缀
// 反推；连接/断开/分屏等运行时路径都直接读 pane.transport，不再依赖前缀。
export function inferTransportFromSessionId(sessionId: string): TerminalTransport {
  if (sessionId.startsWith("serial-")) return "serial";
  if (sessionId.startsWith("local-")) return "local";
  return "ssh";
}

// Split tree types
export type SplitNode =
  | { type: "terminal"; sessionId: string }
  | { type: "pending"; pendingId: string }
  | { type: "connecting"; connectionId: string }
  | {
      type: "split";
      direction: "horizontal" | "vertical";
      ratio: number;
      first: SplitNode;
      second: SplitNode;
    };

export interface TerminalPane {
  sessionId: string;
  transport: TerminalTransport;
  connected: boolean;
  connectedAt: number;
  /** 断开时对最后已知 cwd 的快照。sessionSync 随监听注销被清空,pane 长存,故用于重连恢复目录。 */
  lastCwd?: string;
}

export interface TerminalDirectorySyncState {
  sessionId: string;
  cwd?: string;
  cwdKnown: boolean;
  shell?: string;
  shellType?: string;
  supported: boolean;
  promptReady: boolean;
  promptClean: boolean;
  busy: boolean;
  status: "initializing" | "ready" | "unsupported";
  lastError?: string;
}

export type TerminalDirectoryFollowMode = "off" | "always";

// Business data per terminal tab (split tree, panes, connection state)
export interface TerminalTabData {
  splitTree: SplitNode;
  activePaneId: string;
  panes: Record<string, TerminalPane>;
  directoryFollowMode: TerminalDirectoryFollowMode;
  /** Snippet content to send once the first pane becomes connected. Cleared after write. */
  pendingInput?: string;
}

export interface SSHConnectMetadata {
  host: string;
  port: number;
  username: string;
}

export interface ConnectionLogEntry {
  message: string;
  timestamp: number;
  type: "info" | "error";
}

export type ConnectionStep = "resolve" | "open" | "connect" | "auth" | "shell";

export interface ConnectionState {
  connectionId: string;
  assetId: number;
  assetName: string;
  transport: TerminalTransport;
  password: string;
  /** 本次连接要恢复的目录;重新连接沿用同一个值。 */
  initialWorkdir?: string;
  /** 由 connect() 新开的标签以首个连接 id 为 tab id,连上后要换成会话 id;重连已有标签时不换。 */
  renameTabOnConnect?: boolean;
  logs: ConnectionLogEntry[];
  status: "connecting" | "auth_challenge" | "host_key_verify" | "connected" | "error" | "cancelled";
  currentStep: ConnectionStep;
  error?: string;
  authFailed?: boolean;
  challenge?: {
    challengeId: string;
    /** 服务器发送的挑战名称与说明（Agent 结构化 MFA；可能为空）。 */
    name?: string;
    instruction?: string;
    prompts: string[];
    echo: boolean[];
  };
  hostKeyVerify?: {
    verifyId: string;
    host: string;
    port: number;
    keyType: string;
    fingerprint: string;
    isChanged: boolean;
    oldFingerprint?: string;
  };
}

// Helper: get all session IDs from a split tree (skips pending/connecting)
export function getSessionIds(node: SplitNode): string[] {
  if (node.type === "terminal") return [node.sessionId];
  if (node.type === "pending" || node.type === "connecting") return [];
  return [...getSessionIds(node.first), ...getSessionIds(node.second)];
}

// Helper: all leaf IDs (terminal, pending, connecting) in tree order
function getLeafIds(node: SplitNode): string[] {
  if (node.type === "terminal") return [node.sessionId];
  if (node.type === "pending") return [node.pendingId];
  if (node.type === "connecting") return [node.connectionId];
  return [...getLeafIds(node.first), ...getLeafIds(node.second)];
}

function getConnectingIds(node: SplitNode): string[] {
  if (node.type === "connecting") return [node.connectionId];
  if (node.type === "split") return [...getConnectingIds(node.first), ...getConnectingIds(node.second)];
  return [];
}

function findTabIdByLeaf(tabData: Record<string, TerminalTabData>, leafId: string): string | undefined {
  return Object.keys(tabData).find((id) => getLeafIds(tabData[id].splitTree).includes(leafId));
}

// Helper: replace a leaf node (terminal, pending, or connecting) by ID
function replaceNode(tree: SplitNode, id: string, replacement: SplitNode): SplitNode {
  if (tree.type === "terminal" && tree.sessionId === id) return replacement;
  if (tree.type === "pending" && tree.pendingId === id) return replacement;
  if (tree.type === "connecting" && tree.connectionId === id) return replacement;
  if (tree.type === "split") {
    return {
      ...tree,
      first: replaceNode(tree.first, id, replacement),
      second: replaceNode(tree.second, id, replacement),
    };
  }
  return tree;
}

// Helper: remove a leaf node, collapsing parent split
function removeNode(tree: SplitNode, id: string): SplitNode | null {
  if (tree.type === "terminal" && tree.sessionId === id) return null;
  if (tree.type === "pending" && tree.pendingId === id) return null;
  if (tree.type === "connecting" && tree.connectionId === id) return null;
  if (tree.type === "split") {
    const newFirst = removeNode(tree.first, id);
    const newSecond = removeNode(tree.second, id);
    if (newFirst === null) return newSecond;
    if (newSecond === null) return newFirst;
    if (newFirst === tree.first && newSecond === tree.second) return tree;
    return { ...tree, first: newFirst, second: newSecond };
  }
  return tree;
}

// Helper: update ratio at path
function setRatioAtPath(tree: SplitNode, path: number[], ratio: number): SplitNode {
  if (path.length === 0 && tree.type === "split") {
    return { ...tree, ratio };
  }
  if (tree.type === "split" && path.length > 0) {
    const [head, ...rest] = path;
    if (head === 0) return { ...tree, first: setRatioAtPath(tree.first, rest, ratio) };
    return { ...tree, second: setRatioAtPath(tree.second, rest, ratio) };
  }
  return tree;
}

/** Returns the set of asset IDs that have at least one connected terminal pane. */
export function getTerminalActiveAssetIds(): Set<number> {
  const { tabData } = useTerminalStore.getState();
  const tabs = useTabStore.getState().tabs;
  const ids = new Set<number>();
  for (const tab of tabs) {
    if (tab.type !== "terminal") continue;
    const d = tabData[tab.id];
    if (d && Object.values(d.panes).some((p) => p.connected)) {
      ids.add((tab.meta as TerminalTabMeta).assetId);
    }
  }
  return ids;
}

const syncListeners = new Set<string>();

export function __resetTerminalSyncListenersForTest() {
  syncListeners.clear();
}

function registerSessionSyncListener(sessionId: string) {
  if (!sessionId || syncListeners.has(sessionId)) return;
  syncListeners.add(sessionId);

  const eventName = `ssh:sync:${sessionId}`;
  EventsOn(eventName, (state: TerminalDirectorySyncState) => {
    useTerminalStore.getState().setSessionSyncState(sessionId, state);
  });

  GetSSHSyncState(sessionId)
    .then((state) => {
      if (!syncListeners.has(sessionId)) return;
      useTerminalStore.getState().setSessionSyncState(sessionId, state as TerminalDirectorySyncState);
    })
    .catch(() => {
      /* ignore initial sync fetch errors; live event will reconcile */
    });
}

function unregisterSessionSyncListener(sessionId: string) {
  if (!syncListeners.delete(sessionId)) return;
  EventsOff(`ssh:sync:${sessionId}`);
  useTerminalStore.setState((state) => {
    const next = { ...state.sessionSync };
    delete next[sessionId];
    return { sessionSync: next };
  });
}

// === Connection event listener (shared by connect/reconnect/restore) ===

/**
 * Sets up event listeners for a connection's progress events.
 * Handles progress/error/auth_challenge uniformly; delegates "connected" to callback.
 *
 * @param connectionId - The connection ID from ConnectSSHAsync / ConnectSerialAsync
 * @param onConnected - Called when session is established (receives sessionId)
 * @param onFinished - Optional cleanup called on both "connected" and "error"
 * @param transport - transport 类型，决定事件名前缀（ssh/serial/local）
 */
function setupConnectionListener(
  connectionId: string,
  onConnected: (sessionId: string) => void,
  onFinished?: () => void,
  transport: TerminalTransport = "ssh"
) {
  const eventName = `${TRANSPORTS[transport].eventPrefix}:connect:${connectionId}`;
  EventsOn(
    eventName,
    (event: {
      type: string;
      step?: string;
      message?: string;
      sessionId?: string;
      error?: string;
      authFailed?: boolean;
      challengeId?: string;
      name?: string;
      instruction?: string;
      prompts?: string[];
      echo?: boolean[];
      hostKeyVerifyId?: string;
      hostKeyEvent?: {
        host: string;
        port: number;
        keyType: string;
        fingerprint: string;
        isChanged: boolean;
        oldFingerprint?: string;
      };
    }) => {
      const state = useTerminalStore.getState();
      const conn = state.connections[connectionId];
      if (!conn) return;

      switch (event.type) {
        case "progress":
          useTerminalStore.setState((s) => ({
            connections: {
              ...s.connections,
              [connectionId]: {
                ...s.connections[connectionId],
                currentStep: (event.step as ConnectionStep) || s.connections[connectionId].currentStep,
                logs: [
                  ...s.connections[connectionId].logs,
                  { message: event.message || "", timestamp: Date.now(), type: "info" as const },
                ],
              },
            },
          }));
          break;

        case "connected":
          onConnected(event.sessionId!);
          EventsOff(eventName);
          onFinished?.();
          break;

        case "error":
          useTerminalStore.setState((s) => ({
            connections: {
              ...s.connections,
              [connectionId]: {
                ...s.connections[connectionId],
                status: "error",
                error: event.error,
                authFailed: event.authFailed,
                logs: [
                  ...s.connections[connectionId].logs,
                  { message: event.error || "连接失败", timestamp: Date.now(), type: "error" as const },
                ],
              },
            },
          }));
          onFinished?.();
          break;

        case "auth_challenge":
          useTerminalStore.setState((s) => ({
            connections: {
              ...s.connections,
              [connectionId]: {
                ...s.connections[connectionId],
                status: "auth_challenge",
                challenge: {
                  challengeId: event.challengeId!,
                  name: event.name,
                  instruction: event.instruction,
                  prompts: event.prompts || [],
                  echo: event.echo || [],
                },
                logs: [
                  ...s.connections[connectionId].logs,
                  { message: "等待用户输入认证信息...", timestamp: Date.now(), type: "info" as const },
                ],
              },
            },
          }));
          break;

        case "host_key_verify":
          useTerminalStore.setState((s) => ({
            connections: {
              ...s.connections,
              [connectionId]: {
                ...s.connections[connectionId],
                status: "host_key_verify",
                hostKeyVerify: {
                  verifyId: event.hostKeyVerifyId!,
                  host: event.hostKeyEvent!.host,
                  port: event.hostKeyEvent!.port,
                  keyType: event.hostKeyEvent!.keyType,
                  fingerprint: event.hostKeyEvent!.fingerprint,
                  isChanged: event.hostKeyEvent!.isChanged,
                  oldFingerprint: event.hostKeyEvent!.oldFingerprint,
                },
                logs: [
                  ...s.connections[connectionId].logs,
                  {
                    message: event.hostKeyEvent!.isChanged ? "警告：主机密钥已变更！" : "等待确认主机密钥...",
                    timestamp: Date.now(),
                    type: event.hostKeyEvent!.isChanged ? ("error" as const) : ("info" as const),
                  },
                ],
              },
            },
          }));
          break;
      }
    }
  );
}

// === Pane connection attempts (shared by connect/reconnect/retry) ===
//
// 不变式:分屏树里每个 connecting 节点都有同 id 的 connections 条目,二者同增同删;
// 失败或取消后条目留在 error / cancelled 态,窗格由此给出原因、重新连接与关闭窗格。

type ConnectAttempt = Pick<
  ConnectionState,
  "assetId" | "assetName" | "transport" | "password" | "initialWorkdir" | "renameTabOnConnect"
>;

function newConnectionEntry(connectionId: string, attempt: ConnectAttempt): ConnectionState {
  return { connectionId, ...attempt, logs: [], status: "connecting", currentStep: "resolve" };
}

/** 后端仍在握手(可被 CancelSSHConnect 中止);失败、取消后后端已结束这次连接。 */
function isConnectionInFlight(conn: ConnectionState): boolean {
  return conn.status !== "error" && conn.status !== "cancelled";
}

function connectEventName(conn: ConnectionState): string {
  return `${TRANSPORTS[conn.transport].eventPrefix}:connect:${conn.connectionId}`;
}

let failedAttemptSeq = 0;

/** 把窗格 slotId(终端或连接中节点)原位换成新的连接节点,并移除它原有的窗格/连接条目。 */
function replaceSlotWithConnection(tabId: string, slotId: string, conn: ConnectionState) {
  const state = useTerminalStore.getState();
  const old = state.connections[slotId];
  if (old) {
    if (isConnectionInFlight(old)) CancelSSHConnect(slotId);
    EventsOff(connectEventName(old));
  }
  if (state.tabData[tabId].panes[slotId]) disposeTerminalInstance(slotId);

  useTerminalStore.setState((s) => {
    const d = s.tabData[tabId];
    const panes = { ...d.panes };
    delete panes[slotId];
    const connections = { ...s.connections };
    delete connections[slotId];
    connections[conn.connectionId] = conn;
    return {
      tabData: {
        ...s.tabData,
        [tabId]: {
          ...d,
          splitTree: replaceNode(d.splitTree, slotId, { type: "connecting", connectionId: conn.connectionId }),
          activePaneId: d.activePaneId === slotId ? conn.connectionId : d.activePaneId,
          panes,
        },
      },
      connections,
    };
  });
}

/**
 * 为窗格 slotId 发起一次连接。owningTab 在后端返回时判定这次尝试是否仍被需要
 * (返回所在 tab id),窗格已关闭/取消时返回 undefined,迟到的后端连接随即被取消。
 * 后端直接拒绝时窗格进入 error 态并显示原因。
 */
function startPaneConnection(slotId: string, attempt: ConnectAttempt, owningTab: () => string | undefined) {
  TRANSPORTS[attempt.transport]
    .connectAsync(attempt.assetId, {
      cols: 80,
      rows: 24,
      password: attempt.password,
      initialWorkdir: attempt.initialWorkdir ?? "",
    })
    .then(
      (connectionId: string) => {
        const tabId = owningTab();
        if (!tabId) {
          CancelSSHConnect(connectionId);
          return;
        }
        replaceSlotWithConnection(tabId, slotId, newConnectionEntry(connectionId, attempt));
        watchConnection(connectionId);
      },
      (err: unknown) => {
        console.error("Connect failed:", err);
        const tabId = owningTab();
        if (!tabId) return;
        const message = errorMessage(err);
        replaceSlotWithConnection(tabId, slotId, {
          ...newConnectionEntry(`failed-${++failedAttemptSeq}`, attempt),
          status: "error",
          error: message,
          logs: [{ message, timestamp: Date.now(), type: "error" }],
        });
      }
    );
}

/** 连上后把连接节点换成终端窗格;首个连接新开的标签同时把 tab id 换成会话 id。 */
function attachSession(connectionId: string, sessionId: string) {
  const state = useTerminalStore.getState();
  const conn = state.connections[connectionId];
  const tabId = findTabIdByLeaf(state.tabData, connectionId);
  if (!tabId) return;
  const spec = TRANSPORTS[conn.transport];
  const nextTabId = conn.renameTabOnConnect ? sessionId : tabId;
  const pendingInput = state.tabData[tabId].pendingInput;

  useTerminalStore.setState((s) => {
    const d = s.tabData[tabId];
    const tabData = { ...s.tabData };
    delete tabData[tabId];
    tabData[nextTabId] = {
      ...d,
      splitTree: replaceNode(d.splitTree, connectionId, { type: "terminal", sessionId }),
      activePaneId: sessionId,
      panes: {
        ...d.panes,
        [sessionId]: { sessionId, transport: conn.transport, connected: true, connectedAt: Date.now() },
      },
      // pendingInput 只写一次,见下方
      pendingInput: undefined,
    };
    const connections = { ...s.connections };
    delete connections[connectionId];
    return { tabData, connections };
  });

  if (nextTabId !== tabId) useTabStore.getState().replaceTabId(tabId, nextTabId);
  if (spec.hasDirectorySync) registerSessionSyncListener(sessionId);

  // Write pending snippet input (no trailing \r — user sees content and decides to execute)
  if (pendingInput) {
    spec.write(sessionId, bytesToBase64(new TextEncoder().encode(pendingInput))).catch(console.error);
  }
}

function watchConnection(connectionId: string, onFinished?: () => void) {
  const conn = useTerminalStore.getState().connections[connectionId];
  setupConnectionListener(
    connectionId,
    (sessionId) => attachSession(connectionId, sessionId),
    onFinished,
    conn.transport
  );
}

/** 重连某个终端窗格:原位换成连接节点,失败/取消后在该窗格内可重试。 */
function reconnectPane(tabId: string, sessionId: string) {
  const tab = useTabStore.getState().tabs.find((t) => t.id === tabId);
  if (!tab || tab.type !== "terminal") return;

  const state = useTerminalStore.getState();
  const data = state.tabData[tabId];
  if (!data) return;

  // 连接中/失败的窗格:重新连接即在原窗格重试。
  if (state.connections[sessionId]) {
    state.retryConnect(sessionId);
    return;
  }

  const meta = tab.meta as TerminalTabMeta;
  const pane = data.panes[sessionId];
  const asset = useAssetStore.getState().assets.find((a) => a.ID === meta.assetId);
  // 优先用 pane.transport（运行时权威），否则按资产类型，再退回 session id 前缀（restore 后的兜底）。
  const transport: TerminalTransport =
    pane?.transport ?? (asset ? transportForAsset(asset.Type) : inferTransportFromSessionId(sessionId));

  // 断线重连恢复上次目录:优先取掉线时 markClosed/disconnect 快照在 pane 上的 cwd
  // (那时 sessionSync 已随监听注销被清空);仍连接时(手动重连活会话)回退读 sessionSync。
  // 传给新会话;是否真正 cd 由后端按资产的 RestoreCwdOnReconnect 开关权威决定,cwd 未知即空串。
  const lastCwd = pane?.lastCwd ?? state.sessionSync[sessionId]?.cwd ?? "";

  unregisterSessionSyncListener(sessionId);
  if (pane?.connected) {
    TRANSPORTS[transport].disconnect(sessionId);
  }

  startPaneConnection(
    sessionId,
    { assetId: meta.assetId, assetName: meta.assetName, transport, password: "", initialWorkdir: lastCwd },
    () => findTabIdByLeaf(useTerminalStore.getState().tabData, sessionId)
  );
}

interface TerminalState {
  // Business data keyed by tab id
  tabData: Record<string, TerminalTabData>;
  sessionSync: Record<string, TerminalDirectorySyncState>;
  connectingAssetIds: Set<number>;
  connections: Record<string, ConnectionState>;

  connect: (
    asset: asset_entity.Asset,
    password?: string,
    forceNew?: boolean,
    opts?: { initialInput?: string }
  ) => Promise<string>;
  reconnect: (tabId: string) => void;
  reconnectBySession: (sessionId: string) => void;
  disconnect: (sessionId: string) => void;
  markClosed: (sessionId: string) => void;

  // Connection progress actions
  retryConnect: (connectionId: string, password?: string) => void;
  respondChallenge: (connectionId: string, answers: string[]) => void;
  respondHostKeyVerify: (connectionId: string, action: number) => void;
  /** 中止在途连接;窗格留在 cancelled 态,可重新连接或关闭。 */
  cancelConnect: (connectionId: string) => void;
  /** 关闭连接中/失败的窗格;是标签内唯一窗格时关闭标签。 */
  closeConnectionPane: (connectionId: string) => void;

  // Split pane actions
  setActivePaneId: (tabId: string, paneId: string) => void;
  setSessionSyncState: (sessionId: string, state: TerminalDirectorySyncState) => void;
  setDirectoryFollowMode: (tabId: string, mode: TerminalDirectoryFollowMode) => void;
  splitPane: (tabId: string, direction: "horizontal" | "vertical") => void;
  closePane: (tabId: string, sessionId: string) => void;
  setSplitRatio: (tabId: string, path: number[], ratio: number) => void;
}

export const useTerminalStore = create<TerminalState>((set, get) => ({
  tabData: {},
  sessionSync: {},
  connectingAssetIds: new Set(),
  connections: {},

  connect: async (asset, password = "", forceNew = false, opts) => {
    const assetId = asset.ID;
    const assetPath = useAssetStore.getState().getAssetPath(asset);
    const assetIcon = asset.Icon || "";
    let metadata: SSHConnectMetadata | undefined;
    try {
      const cfg = JSON.parse(asset.Config || "{}");
      metadata = { host: cfg.host || "", port: cfg.port || 22, username: cfg.username || "" };
    } catch {
      /* ignore */
    }

    const tabStore = useTabStore.getState();

    // If there's already a tab for this asset (connected or connecting), switch to it
    if (!forceNew) {
      const existingTab = tabStore.tabs.find((t) => {
        if (t.type !== "terminal") return false;
        const m = t.meta as TerminalTabMeta;
        return m.assetId === assetId;
      });
      if (existingTab) {
        tabStore.activateTab(existingTab.id);
        return existingTab.id;
      }
    }

    set((state) => ({
      connectingAssetIds: new Set(state.connectingAssetIds).add(assetId),
    }));

    try {
      const transport = transportForAsset(asset.Type);
      const spec = TRANSPORTS[transport];
      const connectionId = await spec.connectAsync(assetId, { cols: 80, rows: 24, password });

      // Create tab in tabStore
      tabStore.openTab({
        id: connectionId,
        type: "terminal",
        label: assetPath,
        icon: assetIcon || undefined,
        meta: {
          type: "terminal",
          assetId,
          assetName: assetPath,
          assetIcon: assetIcon || "",
          host: metadata?.host || "",
          port: metadata?.port || 22,
          username: metadata?.username || "",
        },
      });

      // Create business data
      set((state) => ({
        tabData: {
          ...state.tabData,
          [connectionId]: {
            splitTree: { type: "connecting", connectionId },
            activePaneId: connectionId,
            panes: {},
            directoryFollowMode: "off",
            pendingInput: opts?.initialInput,
          },
        },
        connections: {
          ...state.connections,
          [connectionId]: newConnectionEntry(connectionId, {
            assetId,
            assetName: assetPath,
            transport,
            password,
            renameTabOnConnect: true,
          }),
        },
      }));

      // Clear connectingAssetIds on connected or error
      watchConnection(connectionId, () => {
        set((s) => {
          const next = new Set(s.connectingAssetIds);
          next.delete(assetId);
          return { connectingAssetIds: next };
        });
      });

      return connectionId;
    } catch (e) {
      set((state) => {
        const next = new Set(state.connectingAssetIds);
        next.delete(assetId);
        return { connectingAssetIds: next };
      });
      throw e;
    }
  },

  reconnect: (tabId) => {
    const data = get().tabData[tabId];
    if (!data) return;
    reconnectPane(tabId, data.activePaneId);
  },

  reconnectBySession: (sessionId) => {
    const { tabData } = get();
    const tabId = Object.keys(tabData).find((id) => Boolean(tabData[id]?.panes[sessionId]));
    if (tabId) reconnectPane(tabId, sessionId);
  },

  retryConnect: (connectionId, password) => {
    const conn = get().connections[connectionId];
    if (!conn) return;

    // 在原窗格内重试:旧条目先回到 connecting(显示进度与取消),后端返回后由新连接原位接管。
    set((s) => ({
      connections: {
        ...s.connections,
        [connectionId]: { ...conn, status: "connecting", error: undefined, authFailed: undefined },
      },
    }));

    startPaneConnection(
      connectionId,
      {
        assetId: conn.assetId,
        assetName: conn.assetName,
        transport: conn.transport,
        password: password ?? "",
        initialWorkdir: conn.initialWorkdir,
        renameTabOnConnect: conn.renameTabOnConnect,
      },
      () => {
        const s = get();
        // 等待期间被取消或关闭:这次重试作废。
        if (s.connections[connectionId]?.status !== "connecting") return undefined;
        return findTabIdByLeaf(s.tabData, connectionId);
      }
    );

    if (password) {
      UpdateAssetPassword(conn.assetId, password).catch(() => {});
    }
  },

  respondChallenge: (connectionId, answers) => {
    const conn = get().connections[connectionId];
    if (!conn?.challenge) return;

    RespondAuthChallenge(conn.challenge.challengeId, answers);

    set((s) => ({
      connections: {
        ...s.connections,
        [connectionId]: {
          ...s.connections[connectionId],
          status: "connecting",
          challenge: undefined,
        },
      },
    }));
  },

  respondHostKeyVerify: (connectionId, action) => {
    const conn = get().connections[connectionId];
    if (!conn?.hostKeyVerify) return;

    RespondHostKeyVerify(conn.hostKeyVerify.verifyId, action);

    set((s) => ({
      connections: {
        ...s.connections,
        [connectionId]: {
          ...s.connections[connectionId],
          status: "connecting",
          hostKeyVerify: undefined,
          logs: [
            ...s.connections[connectionId].logs,
            {
              message: action === 2 ? "用户拒绝连接" : "主机密钥已确认",
              timestamp: Date.now(),
              type: "info" as const,
            },
          ],
        },
      },
    }));
  },

  cancelConnect: (connectionId) => {
    const conn = get().connections[connectionId];
    if (!conn || !isConnectionInFlight(conn)) return;

    CancelSSHConnect(connectionId);
    EventsOff(connectEventName(conn));

    // 窗格留在 cancelled 态:可在原处重新连接,或关闭窗格。
    set((s) => {
      const next = new Set(s.connectingAssetIds);
      next.delete(conn.assetId);
      return {
        connectingAssetIds: next,
        connections: {
          ...s.connections,
          [connectionId]: { ...conn, status: "cancelled", challenge: undefined, hostKeyVerify: undefined },
        },
      };
    });
  },

  closeConnectionPane: (connectionId) => {
    const state = get();
    const conn = state.connections[connectionId];
    const tabId = findTabIdByLeaf(state.tabData, connectionId);
    if (!conn || !tabId) return;

    const data = state.tabData[tabId];
    const newTree = removeNode(data.splitTree, connectionId);
    // 唯一窗格:关闭整个标签,在途连接由标签关闭钩子取消并清理。
    if (!newTree) {
      useTabStore.getState().closeTab(tabId);
      return;
    }

    if (isConnectionInFlight(conn)) CancelSSHConnect(connectionId);
    EventsOff(connectEventName(conn));

    set((s) => {
      const connections = { ...s.connections };
      delete connections[connectionId];
      const next = new Set(s.connectingAssetIds);
      next.delete(conn.assetId);
      return {
        connections,
        connectingAssetIds: next,
        tabData: {
          ...s.tabData,
          [tabId]: {
            ...data,
            splitTree: newTree,
            activePaneId: data.activePaneId === connectionId ? getLeafIds(newTree)[0] : data.activePaneId,
          },
        },
      };
    });
  },

  disconnect: (sessionId) => {
    // 与 markClosed 一致:清空 sessionSync 前先把 cwd 快照到 pane,供后续重连恢复目录。
    const lastCwd = get().sessionSync[sessionId]?.cwd;
    unregisterSessionSyncListener(sessionId);
    const transport =
      Object.values(get().tabData)
        .map((d) => d.panes[sessionId]?.transport)
        .find((t): t is TerminalTransport => !!t) ?? inferTransportFromSessionId(sessionId);
    TRANSPORTS[transport].disconnect(sessionId);
    set((state) => {
      const newTabData = { ...state.tabData };
      for (const [tabId, data] of Object.entries(newTabData)) {
        if (data.panes[sessionId]) {
          newTabData[tabId] = {
            ...data,
            panes: {
              ...data.panes,
              [sessionId]: { ...data.panes[sessionId], connected: false, ...(lastCwd ? { lastCwd } : {}) },
            },
          };
        }
      }
      return { tabData: newTabData };
    });
  },

  markClosed: (sessionId) => {
    // 快照 cwd 到 pane:必须在 unregisterSessionSyncListener 清空 sessionSync 之前取。
    const lastCwd = get().sessionSync[sessionId]?.cwd;
    unregisterSessionSyncListener(sessionId);
    set((state) => {
      const newTabData = { ...state.tabData };
      for (const [tabId, data] of Object.entries(newTabData)) {
        if (data.panes[sessionId]) {
          newTabData[tabId] = {
            ...data,
            panes: {
              ...data.panes,
              [sessionId]: { ...data.panes[sessionId], connected: false, ...(lastCwd ? { lastCwd } : {}) },
            },
          };
        }
      }
      return { tabData: newTabData };
    });
  },

  setActivePaneId: (tabId, paneId) => {
    set((state) => {
      const data = state.tabData[tabId];
      if (!data) return state;
      return {
        tabData: { ...state.tabData, [tabId]: { ...data, activePaneId: paneId } },
      };
    });
  },

  setSessionSyncState: (sessionId, syncState) => {
    set((state) => ({
      sessionSync: {
        ...state.sessionSync,
        [sessionId]: syncState,
      },
    }));
  },

  setDirectoryFollowMode: (tabId, mode) => {
    set((state) => {
      const data = state.tabData[tabId];
      if (!data) return state;
      return {
        tabData: {
          ...state.tabData,
          [tabId]: {
            ...data,
            directoryFollowMode: mode,
          },
        },
      };
    });
  },

  splitPane: (tabId, direction) => {
    const data = get().tabData[tabId];
    if (!data) return;
    // 能否分屏按 transport 查表:ssh 复用连接开新会话,local 再起一个同 shell 的 PTY,
    // serial 物理端口不可复用故不支持。上游菜单/工具栏已 disable,这里再防一道,
    // 避免快捷键等外部入口绕过。
    const activeTransport = data.panes[data.activePaneId]?.transport ?? "ssh";
    const spec = TRANSPORTS[activeTransport];
    if (!spec.canSplit || !spec.split) return;

    const pendingId = `pending-${Date.now()}`;

    // Step 1: Split UI with pending placeholder
    set((state) => {
      const d = state.tabData[tabId];
      if (!d) return state;

      const newTree = replaceNode(d.splitTree, d.activePaneId, {
        type: "split",
        direction,
        ratio: 0.5,
        first: { type: "terminal", sessionId: d.activePaneId },
        second: { type: "pending", pendingId },
      });

      return {
        tabData: { ...state.tabData, [tabId]: { ...d, splitTree: newTree } },
      };
    });

    // Step 2: Create the new session (ssh: reuse connection / local: new PTY)
    spec
      .split(data.activePaneId, 80, 24)
      .then((sessionId: string) => {
        set((state) => {
          const d = state.tabData[tabId];
          if (!d) return state;

          const newTree = replaceNode(d.splitTree, pendingId, {
            type: "terminal",
            sessionId,
          });

          return {
            tabData: {
              ...state.tabData,
              [tabId]: {
                ...d,
                splitTree: newTree,
                activePaneId: sessionId,
                panes: {
                  ...d.panes,
                  // 新 pane 继承 active pane 的 transport（ssh / local 均可 split，
                  // 各自得到正确的 transport，分屏出的 pane 走对应的读写/事件通道）。
                  [sessionId]: { sessionId, transport: activeTransport, connected: true, connectedAt: Date.now() },
                },
              },
            },
          };
        });
        // 仅有目录同步能力的 transport(ssh)才挂 sync 监听;local 无 cwd 同步,避免空挂 ssh:sync。
        if (spec.hasDirectorySync) {
          registerSessionSyncListener(sessionId);
        }
      })
      .catch((err: unknown) => {
        console.error("Split connection failed:", err);
        // 标签已在等待期间关闭:没有窗格可留。
        const tab = useTabStore.getState().tabs.find((t) => t.id === tabId);
        if (!tab || !get().tabData[tabId]) return;
        // 新窗格留在失败态:显示原因,「重新连接」在原处对同一资产发起连接,「关闭窗格」移除它。
        const meta = tab.meta as TerminalTabMeta;
        const message = errorMessage(err);
        replaceSlotWithConnection(tabId, pendingId, {
          ...newConnectionEntry(`failed-${++failedAttemptSeq}`, {
            assetId: meta.assetId,
            assetName: meta.assetName,
            transport: activeTransport,
            password: "",
          }),
          status: "error",
          error: message,
          logs: [{ message, timestamp: Date.now(), type: "error" }],
        });
      });
  },

  closePane: (tabId, sessionId) => {
    const data = get().tabData[tabId];
    if (!data) return;

    // Last pane (counting connecting/failed panes): closing it closes the tab. tabStore may ask first
    // (in-flight transfers), and its close hook disconnects every pane — so nothing is torn down here.
    const newTree = removeNode(data.splitTree, sessionId);
    if (!newTree) {
      useTabStore.getState().closeTab(tabId);
      return;
    }

    const pane = data.panes[sessionId];
    unregisterSessionSyncListener(sessionId);
    if (pane?.connected) {
      TRANSPORTS[pane.transport].disconnect(sessionId);
    }

    const newActivePaneId = data.activePaneId === sessionId ? getLeafIds(newTree)[0] : data.activePaneId;

    const newPanes = { ...data.panes };
    delete newPanes[sessionId];

    set((state) => ({
      tabData: {
        ...state.tabData,
        [tabId]: {
          ...data,
          splitTree: newTree,
          activePaneId: newActivePaneId,
          panes: newPanes,
        },
      },
    }));

    // Drop the persistent xterm now that it's no longer in the tree.
    disposeTerminalInstance(sessionId);
  },

  setSplitRatio: (tabId, path, ratio) => {
    set((state) => {
      const data = state.tabData[tabId];
      if (!data) return state;
      return {
        tabData: {
          ...state.tabData,
          [tabId]: { ...data, splitTree: setRatioAtPath(data.splitTree, path, ratio) },
        },
      };
    });
  },
}));

// === Close Hook: clean up when tabStore closes a terminal tab ===

registerTabCloseHook((tab) => {
  if (tab.type !== "terminal") return;

  const state = useTerminalStore.getState();
  const data = state.tabData[tab.id];

  // Cancel every pane still connecting (first connect, reconnect or retry) and drop its entry.
  const conns = data
    ? getConnectingIds(data.splitTree).flatMap((id) => (state.connections[id] ? [state.connections[id]] : []))
    : [];
  for (const conn of conns) {
    if (isConnectionInFlight(conn)) CancelSSHConnect(conn.connectionId);
    EventsOff(connectEventName(conn));
  }

  // Disconnect all panes and drop their persistent xterm instances.
  if (data) {
    for (const pane of Object.values(data.panes)) {
      unregisterSessionSyncListener(pane.sessionId);
      if (pane.connected) {
        TRANSPORTS[pane.transport].disconnect(pane.sessionId);
      }
      disposeTerminalInstance(pane.sessionId);
    }
  }

  // Clean up state
  useTerminalStore.setState((s) => {
    const newTabData = { ...s.tabData };
    delete newTabData[tab.id];
    const newConnections = { ...s.connections };
    const next = new Set(s.connectingAssetIds);
    for (const conn of conns) {
      delete newConnections[conn.connectionId];
      next.delete(conn.assetId);
    }
    return { tabData: newTabData, connections: newConnections, connectingAssetIds: next };
  });
});

// === Restore Hook: initialize tabData + auto-reconnect ===

registerTabRestoreHook("terminal", (tabs) => {
  if (tabs.length === 0) return;

  // Initialize tabData as disconnected (reconnect will transition to connecting)
  const tabData: Record<string, TerminalTabData> = {};
  for (const tab of tabs) {
    // 恢复阶段还没有 pane.transport，只能从 session id 前缀反推；reconnect 跑完后
    // 新 pane 会带上权威 transport，这个值随之被覆盖。
    const transport = inferTransportFromSessionId(tab.id);
    tabData[tab.id] = {
      splitTree: { type: "terminal", sessionId: tab.id },
      activePaneId: tab.id,
      panes: { [tab.id]: { sessionId: tab.id, transport, connected: false, connectedAt: 0 } },
      directoryFollowMode: "off",
    };
  }
  useTerminalStore.setState({ tabData });

  // Auto-reconnect each terminal tab
  for (const tab of tabs) {
    useTerminalStore.getState().reconnect(tab.id);
  }
});
