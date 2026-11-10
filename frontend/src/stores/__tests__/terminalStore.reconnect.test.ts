import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { CancelSSHConnect, ConnectSSHAsync, SplitSSH } from "../../../wailsjs/go/ssh/SSH";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { asset_entity } from "../../../wailsjs/go/models";
import { useTabStore, type Tab } from "../tabStore";
import { useAssetStore } from "../assetStore";
import { __resetTerminalSyncListenersForTest, useTerminalStore, type SplitNode } from "../terminalStore";

// 终端窗格的连接状态机:任何一次失败 / 取消之后,窗格里必须有一个可操作的连接条目
// (失败原因 + 重新连接 + 关闭窗格)。分屏树里出现 connecting 节点却没有对应 connections
// 条目,ConnectionProgress 就只能画一个无操作的转圈 —— 这正是本组测试要挡住的状态。

const handlers = new Map<string, (event: unknown) => void>();

function emit(eventName: string, event: unknown) {
  const handler = handlers.get(eventName);
  if (!handler) throw new Error(`no listener for ${eventName}`);
  handler(event);
}

function terminalTab(id: string, assetId = 1): Tab {
  return {
    id,
    type: "terminal",
    label: `Server ${assetId}`,
    meta: {
      type: "terminal",
      assetId,
      assetName: `Server ${assetId}`,
      assetIcon: "",
      host: "10.0.0.1",
      port: 22,
      username: "root",
    },
  };
}

function sshAsset(id = 1): asset_entity.Asset {
  return new asset_entity.Asset({ ID: id, Name: `Server ${id}`, Type: "ssh", Config: "{}" });
}

function connectingLeaves(node: SplitNode): string[] {
  if (node.type === "connecting") return [node.connectionId];
  if (node.type === "split") return [...connectingLeaves(node.first), ...connectingLeaves(node.second)];
  return [];
}

/** 每个 connecting 节点都必须有连接条目,否则界面只剩无操作的转圈。 */
function expectNoOrphanConnectingNodes() {
  const { tabData, connections } = useTerminalStore.getState();
  for (const data of Object.values(tabData)) {
    for (const id of connectingLeaves(data.splitTree)) {
      expect(connections[id], `connecting node ${id} has no connection entry`).toBeDefined();
    }
  }
}

function onlyConnectingId(tabId: string): string {
  const data = useTerminalStore.getState().tabData[tabId];
  expect(data).toBeDefined();
  const ids = connectingLeaves(data.splitTree);
  expect(ids).toHaveLength(1);
  return ids[0];
}

/** 一个已连上的单窗格标签:tab id 是首个会话 id(connect 成功后 replaceTabId 的结果)。 */
function seedConnectedTab(tabId = "s1") {
  useTabStore.setState({ tabs: [terminalTab(tabId)], activeTabId: tabId });
  useTerminalStore.setState({
    tabData: {
      [tabId]: {
        splitTree: { type: "terminal", sessionId: tabId },
        activePaneId: tabId,
        panes: { [tabId]: { sessionId: tabId, transport: "ssh", connected: false, connectedAt: 1 } },
        directoryFollowMode: "off",
      },
    },
  });
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

beforeEach(() => {
  __resetTerminalSyncListenersForTest();
  handlers.clear();
  vi.clearAllMocks();
  vi.mocked(ConnectSSHAsync).mockReset();
  vi.mocked(EventsOn).mockImplementation((eventName, handler) => {
    handlers.set(eventName, handler as (event: unknown) => void);
    return vi.fn();
  });
  vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Server 1");
  useAssetStore.setState({ assets: [sshAsset(1)] });
  useTabStore.setState({ tabs: [], activeTabId: null });
  useTerminalStore.setState({ tabData: {}, sessionSync: {}, connections: {}, connectingAssetIds: new Set() });
});

afterEach(() => {
  __resetTerminalSyncListenersForTest();
});

describe("重连失败后重新连接", () => {
  it("在原窗格内重试:新连接接管同一个窗格,不会留下没有连接条目的转圈节点", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();
    emit("ssh:connect:conn-2", { type: "error", error: "dial tcp: i/o timeout" });

    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-3");
    useTerminalStore.getState().retryConnect("conn-2");
    await flush();

    expectNoOrphanConnectingNodes();
    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["s1"]);
    const retryId = onlyConnectingId("s1");
    expect(useTerminalStore.getState().connections[retryId].status).toBe("connecting");

    emit(`ssh:connect:${retryId}`, { type: "connected", sessionId: "s4" });
    const data = useTerminalStore.getState().tabData.s1;
    expect(data.splitTree).toEqual({ type: "terminal", sessionId: "s4" });
    expect(data.panes.s4?.connected).toBe(true);
  });

  it("首次连接失败后重试也在原标签内进行,连上后标签换成会话 id", async () => {
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-1");
    await useTerminalStore.getState().connect(sshAsset(1));
    emit("ssh:connect:conn-1", { type: "error", error: "auth failed" });

    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().retryConnect("conn-1");
    await flush();

    expectNoOrphanConnectingNodes();
    expect(useTabStore.getState().tabs).toHaveLength(1);
    emit("ssh:connect:conn-2", { type: "connected", sessionId: "s9" });

    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["s9"]);
    expect(useTerminalStore.getState().tabData.s9.splitTree).toEqual({ type: "terminal", sessionId: "s9" });
    expect(useTerminalStore.getState().connections).toEqual({});
  });

  it("重试本身被后端拒绝时,窗格停在失败态并显示原因", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();
    emit("ssh:connect:conn-2", { type: "error", error: "timeout" });

    vi.mocked(ConnectSSHAsync).mockRejectedValueOnce(new Error("资产不存在"));
    useTerminalStore.getState().retryConnect("conn-2");
    await flush();

    expectNoOrphanConnectingNodes();
    const id = onlyConnectingId("s1");
    expect(useTerminalStore.getState().connections[id]).toMatchObject({ status: "error", error: "资产不存在" });
  });
});

describe("重连发起即被拒绝", () => {
  it("恢复标签时后端拒绝重连,窗格显示失败原因而不是一块空终端", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockRejectedValueOnce(new Error("资产不存在"));

    useTerminalStore.getState().reconnect("s1");
    await flush();

    expectNoOrphanConnectingNodes();
    const id = onlyConnectingId("s1");
    expect(useTerminalStore.getState().connections[id]).toMatchObject({ status: "error", error: "资产不存在" });
  });
});

describe("取消连接", () => {
  it("取消重连后窗格可重试、可关闭,不会停在转圈", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();

    useTerminalStore.getState().cancelConnect("conn-2");

    expect(CancelSSHConnect).toHaveBeenCalledWith("conn-2");
    expectNoOrphanConnectingNodes();
    const id = onlyConnectingId("s1");
    expect(useTerminalStore.getState().connections[id].status).toBe("cancelled");
    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["s1"]);
  });

  it("取消首次连接后标签仍在,可在原处重试", async () => {
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-1");
    await useTerminalStore.getState().connect(sshAsset(1));

    useTerminalStore.getState().cancelConnect("conn-1");

    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["conn-1"]);
    expect(useTerminalStore.getState().connections["conn-1"].status).toBe("cancelled");
    expect(useTerminalStore.getState().connectingAssetIds.has(1)).toBe(false);
  });

  it("重试发起期间取消,迟到的新连接被取消且不接管窗格", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();
    emit("ssh:connect:conn-2", { type: "error", error: "timeout" });

    let resolveRetry: (id: string) => void = () => {};
    vi.mocked(ConnectSSHAsync).mockReturnValueOnce(new Promise<string>((r) => (resolveRetry = r)));
    useTerminalStore.getState().retryConnect("conn-2");
    useTerminalStore.getState().cancelConnect("conn-2");
    resolveRetry("conn-3");
    await flush();

    expect(CancelSSHConnect).toHaveBeenCalledWith("conn-3");
    expect(onlyConnectingId("s1")).toBe("conn-2");
    expect(useTerminalStore.getState().connections["conn-2"].status).toBe("cancelled");
  });
});

describe("关闭连接中的窗格", () => {
  it("唯一窗格:关闭即关闭标签", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();
    emit("ssh:connect:conn-2", { type: "error", error: "timeout" });

    useTerminalStore.getState().closeConnectionPane("conn-2");

    expect(useTabStore.getState().tabs).toEqual([]);
    expect(useTerminalStore.getState().tabData).toEqual({});
    expect(useTerminalStore.getState().connections).toEqual({});
  });

  it("分屏中:只移除该窗格,其余窗格保留并获得焦点", async () => {
    useTabStore.setState({ tabs: [terminalTab("s1")], activeTabId: "s1" });
    useTerminalStore.setState({
      tabData: {
        s1: {
          splitTree: {
            type: "split",
            direction: "horizontal",
            ratio: 0.5,
            first: { type: "terminal", sessionId: "s1" },
            second: { type: "terminal", sessionId: "s2" },
          },
          activePaneId: "s2",
          panes: {
            s1: { sessionId: "s1", transport: "ssh", connected: true, connectedAt: 1 },
            s2: { sessionId: "s2", transport: "ssh", connected: false, connectedAt: 1 },
          },
          directoryFollowMode: "off",
        },
      },
    });
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();
    emit("ssh:connect:conn-2", { type: "error", error: "timeout" });

    useTerminalStore.getState().closeConnectionPane("conn-2");

    const data = useTerminalStore.getState().tabData.s1;
    expect(data.splitTree).toEqual({ type: "terminal", sessionId: "s1" });
    expect(data.activePaneId).toBe("s1");
    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["s1"]);
    expect(useTerminalStore.getState().connections).toEqual({});
  });

  it("关闭终端窗格时,旁边失败的连接窗格算作另一个窗格,标签不被关闭", async () => {
    useTabStore.setState({ tabs: [terminalTab("s1")], activeTabId: "s1" });
    useTerminalStore.setState({
      tabData: {
        s1: {
          splitTree: {
            type: "split",
            direction: "horizontal",
            ratio: 0.5,
            first: { type: "terminal", sessionId: "s1" },
            second: { type: "terminal", sessionId: "s2" },
          },
          activePaneId: "s2",
          panes: {
            s1: { sessionId: "s1", transport: "ssh", connected: true, connectedAt: 1 },
            s2: { sessionId: "s2", transport: "ssh", connected: false, connectedAt: 1 },
          },
          directoryFollowMode: "off",
        },
      },
    });
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();
    emit("ssh:connect:conn-2", { type: "error", error: "timeout" });

    useTerminalStore.getState().closePane("s1", "s1");

    expect(useTabStore.getState().tabs.map((t) => t.id)).toEqual(["s1"]);
    const data = useTerminalStore.getState().tabData.s1;
    expect(data.splitTree).toEqual({ type: "connecting", connectionId: "conn-2" });
    expect(data.activePaneId).toBe("conn-2");
    expectNoOrphanConnectingNodes();
  });

  it("关闭仍在连接中的窗格会取消后端连接", async () => {
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-1");
    await useTerminalStore.getState().connect(sshAsset(1));

    useTerminalStore.getState().closeConnectionPane("conn-1");

    expect(CancelSSHConnect).toHaveBeenCalledWith("conn-1");
    expect(useTabStore.getState().tabs).toEqual([]);
  });
});

describe("分屏里按会话重连", () => {
  it("掉线的是非活动窗格时,重连的是掉线的那个窗格", async () => {
    useTabStore.setState({ tabs: [terminalTab("s1")], activeTabId: "s1" });
    useTerminalStore.setState({
      tabData: {
        s1: {
          splitTree: {
            type: "split",
            direction: "horizontal",
            ratio: 0.5,
            first: { type: "terminal", sessionId: "s1" },
            second: { type: "terminal", sessionId: "s2" },
          },
          activePaneId: "s1",
          panes: {
            s1: { sessionId: "s1", transport: "ssh", connected: true, connectedAt: 1 },
            s2: { sessionId: "s2", transport: "ssh", connected: false, connectedAt: 1 },
          },
          directoryFollowMode: "off",
        },
      },
    });
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");

    useTerminalStore.getState().reconnectBySession("s2");
    await flush();

    const tree = useTerminalStore.getState().tabData.s1.splitTree;
    expect(tree).toMatchObject({
      first: { type: "terminal", sessionId: "s1" },
      second: { type: "connecting", connectionId: "conn-2" },
    });
  });
});

describe("关闭正在重连的标签", () => {
  it("取消标签内所有在途连接并清掉条目", async () => {
    seedConnectedTab("s1");
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-2");
    useTerminalStore.getState().reconnect("s1");
    await flush();

    useTabStore.getState().closeTab("s1");

    expect(CancelSSHConnect).toHaveBeenCalledWith("conn-2");
    expect(useTerminalStore.getState().connections).toEqual({});
  });
});

describe("分屏失败", () => {
  it("新窗格停在失败态并显示原因,可在原处重新连接", async () => {
    useTabStore.setState({ tabs: [terminalTab("s1")], activeTabId: "s1" });
    useTerminalStore.setState({
      tabData: {
        s1: {
          splitTree: { type: "terminal", sessionId: "s1" },
          activePaneId: "s1",
          panes: { s1: { sessionId: "s1", transport: "ssh", connected: true, connectedAt: 1 } },
          directoryFollowMode: "off",
        },
      },
    });
    vi.mocked(SplitSSH).mockRejectedValueOnce(new Error("ssh: open channel: administratively prohibited"));

    useTerminalStore.getState().splitPane("s1", "vertical");
    await flush();

    const failedId = onlyConnectingId("s1");
    const conn = useTerminalStore.getState().connections[failedId];
    expect(conn.status).toBe("error");
    expect(conn.error).toContain("administratively prohibited");
    expectNoOrphanConnectingNodes();

    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-9");
    useTerminalStore.getState().retryConnect(failedId);
    await flush();
    emit("ssh:connect:conn-9", { type: "connected", sessionId: "s2" });

    const data = useTerminalStore.getState().tabData.s1;
    expect(data.splitTree).toMatchObject({
      type: "split",
      first: { type: "terminal", sessionId: "s1" },
      second: { type: "terminal", sessionId: "s2" },
    });
  });
});
