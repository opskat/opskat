import { describe, it, expect, vi, afterEach } from "vitest";
import type { Tab } from "@/stores/tabStore";
import {
  getTabConnectionPhase,
  isAutoReconnectPhase,
  pickExistingAssetTab,
  pickReconnectTargetTab,
  reconnectTab,
  reconnectTabIfNeeded,
  registerLiveTabReconnector,
  registerTabReconnectAdapter,
  type TabConnectionPhase,
} from "../tabReconnect";

function makeTab(id: string, pageId = "test-page"): Tab {
  return {
    id,
    type: "page",
    label: id,
    meta: { type: "page", pageId, assetId: 99 },
  };
}

describe("tabReconnect registry", () => {
  const cleanups: Array<() => void> = [];

  afterEach(() => {
    while (cleanups.length > 0) cleanups.pop()?.();
  });

  it("classifies only failed, cancelled, and disconnected as auto-reconnect phases", () => {
    const auto: TabConnectionPhase[] = ["failed", "cancelled", "disconnected"];
    const keep: Array<TabConnectionPhase | null> = ["connected", "connecting", "waiting_input", null];
    for (const phase of auto) expect(isAutoReconnectPhase(phase)).toBe(true);
    for (const phase of keep) expect(isAutoReconnectPhase(phase)).toBe(false);
  });

  it("prefers live handle over store adapter and falls back when live handle unmounts", async () => {
    const tab = makeTab("live-vs-adapter-1", "test-live-vs-adapter");
    const adapterReconnect = vi.fn();
    const liveReconnect = vi.fn();

    cleanups.push(
      registerTabReconnectAdapter({
        match: (t) => t.id === tab.id,
        getPhase: () => "connected",
        reconnect: adapterReconnect,
      })
    );
    const unregisterLive = registerLiveTabReconnector(tab.id, {
      getPhase: () => "failed",
      reconnect: liveReconnect,
    });
    cleanups.push(unregisterLive);

    expect(getTabConnectionPhase(tab)).toBe("failed");
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(liveReconnect).toHaveBeenCalledTimes(1);
    expect(adapterReconnect).not.toHaveBeenCalled();

    unregisterLive();
    expect(getTabConnectionPhase(tab)).toBe("connected");
    expect(await reconnectTabIfNeeded(tab)).toBe(false);
    expect(await reconnectTab(tab)).toBe(true);
    expect(adapterReconnect).toHaveBeenCalledTimes(1);
  });

  it("does not let a stale StrictMode cleanup remove a newer live handle for the same tabId", () => {
    const tab = makeTab("strict-mode-tab", "test-strict");
    const unregisterOld = registerLiveTabReconnector(tab.id, {
      getPhase: () => "failed",
      reconnect: vi.fn(),
    });
    const unregisterNew = registerLiveTabReconnector(tab.id, {
      getPhase: () => "connected",
      reconnect: vi.fn(),
    });
    cleanups.push(unregisterNew);

    unregisterOld();
    expect(getTabConnectionPhase(tab)).toBe("connected");
  });

  it("reconnectTabIfNeeded skips connecting, waiting_input, and connected, while reconnectTab forces reconnect", async () => {
    const tab = makeTab("phase-guard-tab", "test-phase-guard");
    let phase: TabConnectionPhase = "connecting";
    const reconnect = vi.fn();

    cleanups.push(
      registerLiveTabReconnector(tab.id, {
        getPhase: () => phase,
        reconnect,
      })
    );

    for (const p of ["connecting", "waiting_input", "connected"] as const) {
      phase = p;
      expect(await reconnectTabIfNeeded(tab)).toBe(false);
    }
    expect(reconnect).not.toHaveBeenCalled();

    phase = "connecting";
    expect(await reconnectTab(tab)).toBe(true);
    expect(reconnect).toHaveBeenCalledTimes(1);

    phase = "disconnected";
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(reconnect).toHaveBeenCalledTimes(2);
  });

  it("pickExistingAssetTab prioritizes active tab, then healthy/in-progress tab, then failed tab", () => {
    const failedTab = makeTab("pick-failed");
    const connectingTab = makeTab("pick-connecting");
    const disconnectedTab = makeTab("pick-disconnected");

    cleanups.push(
      registerLiveTabReconnector(failedTab.id, { getPhase: () => "failed", reconnect: vi.fn() }),
      registerLiveTabReconnector(connectingTab.id, { getPhase: () => "connecting", reconnect: vi.fn() }),
      registerLiveTabReconnector(disconnectedTab.id, { getPhase: () => "disconnected", reconnect: vi.fn() })
    );

    // 1. Active tab wins even if another tab is healthy/connecting
    expect(pickExistingAssetTab([failedTab, connectingTab], failedTab.id)?.id).toBe(failedTab.id);
    // 2. When none is active, healthy/in-progress beats failed
    expect(pickExistingAssetTab([failedTab, connectingTab], "other-tab")?.id).toBe(connectingTab.id);
    // 3. When all matching tabs are failed/disconnected, pick the first one
    expect(pickExistingAssetTab([failedTab, disconnectedTab], "other-tab")?.id).toBe(failedTab.id);
  });

  it("pickReconnectTargetTab prioritizes active tab, then non-connected tab, then first tab", () => {
    const connectedTab = makeTab("reconn-connected");
    const connectingTab = makeTab("reconn-connecting");
    const connectedTab2 = makeTab("reconn-connected-2");

    cleanups.push(
      registerLiveTabReconnector(connectedTab.id, { getPhase: () => "connected", reconnect: vi.fn() }),
      registerLiveTabReconnector(connectingTab.id, { getPhase: () => "connecting", reconnect: vi.fn() }),
      registerLiveTabReconnector(connectedTab2.id, { getPhase: () => "connected", reconnect: vi.fn() })
    );

    expect(pickReconnectTargetTab([connectedTab, connectingTab], connectedTab.id)?.id).toBe(connectedTab.id);
    expect(pickReconnectTargetTab([connectedTab, connectingTab], "other-tab")?.id).toBe(connectingTab.id);
    expect(pickReconnectTargetTab([connectedTab, connectedTab2], "other-tab")?.id).toBe(connectedTab.id);
  });
});
