import type { Tab } from "@/stores/tabStore";

/**
 * 统一标签页连接阶段：由各领域 store（适配器）或活跃面板实例（Live handle）上报，
 * 使资产打开/重连逻辑无需在共享层按资产类型字符串分支（OCP）。
 */
export type TabConnectionPhase = "connected" | "connecting" | "waiting_input" | "failed" | "cancelled" | "disconnected";

export interface TabReconnectAdapter {
  match: (tab: Tab) => boolean;
  getPhase: (tab: Tab) => TabConnectionPhase;
  reconnect: (tab: Tab) => void | Promise<void>;
}

export interface LiveTabReconnector {
  getPhase: () => TabConnectionPhase;
  reconnect: () => void | Promise<void>;
}

const adapters: TabReconnectAdapter[] = [];
const liveReconnectors = new Map<string, LiveTabReconnector>();

/** 注册领域 store 级重连适配器（如 terminalStore / queryStore / k8sStore 等）。 */
export function registerTabReconnectAdapter(adapter: TabReconnectAdapter): () => void {
  adapters.push(adapter);
  return () => {
    const idx = adapters.indexOf(adapter);
    if (idx !== -1) adapters.splice(idx, 1);
  };
}

/**
 * 注册活跃视图实例级重连句柄（用于持有 view-local IPC 生命周期的面板，如 RDPPanel / VNCPanel）。
 * 组件 mount 时注册、unmount 时调用返回函数注销；同引用校验兼容 React StrictMode 双挂载。
 */
export function registerLiveTabReconnector(tabId: string, handle: LiveTabReconnector): () => void {
  liveReconnectors.set(tabId, handle);
  return () => {
    if (liveReconnectors.get(tabId) === handle) {
      liveReconnectors.delete(tabId);
    }
  };
}

/** 读取标签页当前连接阶段；未注册的标签（如扩展自定义页、普通信息页）返回 null。 */
export function getTabConnectionPhase(tab: Tab): TabConnectionPhase | null {
  const live = liveReconnectors.get(tab.id);
  if (live) return live.getPhase();
  const adapter = adapters.find((entry) => entry.match(tab));
  return adapter ? adapter.getPhase(tab) : null;
}

/** 失败、取消或已断开三种非存活态在再次打开资产时自动触发重连。 */
export function isAutoReconnectPhase(phase: TabConnectionPhase | null): boolean {
  return phase === "failed" || phase === "cancelled" || phase === "disconnected";
}

/** 显式触发指定标签页重连（无视当前阶段，用于右键「重新连接」等显式动作）。 */
export async function reconnectTab(tab: Tab): Promise<boolean> {
  const live = liveReconnectors.get(tab.id);
  if (live) {
    await live.reconnect();
    return true;
  }
  const adapter = adapters.find((entry) => entry.match(tab));
  if (!adapter) return false;
  await adapter.reconnect(tab);
  return true;
}

/** 仅当标签页处于失败 / 取消 / 已断开态时触发重连；连接中、等待用户输入及已连接态保持不动。 */
export async function reconnectTabIfNeeded(tab: Tab): Promise<boolean> {
  const phase = getTabConnectionPhase(tab);
  if (!isAutoReconnectPhase(phase)) return false;
  return reconnectTab(tab);
}

/**
 * 同一资产存在多个打开标签页时的选择优先级（普通打开 / 双击）：
 * 1. 当前激活标签若属于该资产，优先命中它；
 * 2. 否则优先聚焦首个健康 / 进行中标签（不打扰正在工作的会话或连接中状态）；
 * 3. 否则命中首个失败 / 取消 / 已断开标签（激活并重连）。
 */
export function pickExistingAssetTab(matchingTabs: Tab[], activeTabId: string | null): Tab | undefined {
  if (matchingTabs.length === 0) return undefined;
  if (activeTabId) {
    const active = matchingTabs.find((tab) => tab.id === activeTabId);
    if (active) return active;
  }
  const healthyOrInProgress = matchingTabs.find((tab) => !isAutoReconnectPhase(getTabConnectionPhase(tab)));
  return healthyOrInProgress ?? matchingTabs[0];
}

/**
 * 显式「重新连接」动作在同资产多标签下的选择优先级：
 * 1. 当前激活标签若属于该资产，优先重连它；
 * 2. 否则优先挑选非已连接标签（失败 / 断开 / 取消 / 连接中）；
 * 3. 否则回落首个匹配标签。
 */
export function pickReconnectTargetTab(matchingTabs: Tab[], activeTabId: string | null): Tab | undefined {
  if (matchingTabs.length === 0) return undefined;
  if (activeTabId) {
    const active = matchingTabs.find((tab) => tab.id === activeTabId);
    if (active) return active;
  }
  const nonConnected = matchingTabs.find((tab) => getTabConnectionPhase(tab) !== "connected");
  return nonConnected ?? matchingTabs[0];
}
