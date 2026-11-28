import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type { asset_entity } from "../../../wailsjs/go/models";
import type { Tab } from "@/stores/tabStore";

const openQueryTab = vi.fn();
const connect = vi.fn().mockResolvedValue("conn-1");
const openTab = vi.fn();
const activateTab = vi.fn();
let mockTabs: Tab[] = [];
let mockActiveTabId: string | null = null;

vi.mock("@/stores/queryStore", () => ({ useQueryStore: { getState: () => ({ openQueryTab }) } }));
vi.mock("@/stores/terminalStore", () => ({ useTerminalStore: { getState: () => ({ connect }) } }));
vi.mock("@/stores/tabStore", () => ({
  useTabStore: {
    getState: () => ({ tabs: mockTabs, activeTabId: mockActiveTabId, openTab, activateTab }),
  },
}));
import { findExistingAssetTabs, openAssetConnection, reconnectAssetConnection } from "../openAsset";
import { registerLiveTabReconnector, type TabConnectionPhase } from "../tabReconnect";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "@/extension/assetTypes";
import type { ExtManifest } from "@/extension/types";

function makeAsset(id: number, name: string, type: string): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: type,
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: "",
    CmdPolicy: "",
    SortOrder: 0,
    sshTunnelId: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  };
}

describe("openAssetConnection", () => {
  const cleanups: Array<() => void> = [];

  beforeEach(() => {
    vi.clearAllMocks();
    mockTabs = [];
    mockActiveTabId = null;
  });

  afterEach(() => {
    while (cleanups.length > 0) cleanups.pop()?.();
  });

  it("opens a query tab for a database asset", async () => {
    await openAssetConnection(makeAsset(1, "db", "database"));
    expect(openQueryTab).toHaveBeenCalledTimes(1);
  });

  it("connects a terminal for an ssh asset", async () => {
    await openAssetConnection(makeAsset(2, "web", "ssh"));
    expect(connect).toHaveBeenCalledTimes(1);
  });

  it("opens a page tab for an rdp asset", async () => {
    await openAssetConnection(makeAsset(3, "desktop", "rdp"));

    expect(openTab).toHaveBeenCalledWith({
      id: "rdp-3",
      type: "page",
      label: "desktop",
      icon: "monitor",
      meta: { type: "page", pageId: "rdp", assetId: 3 },
    });
    expect(connect).not.toHaveBeenCalled();
  });

  // k8s 的派发曾经是一个写死的 `asset.Type === "k8s"` 分支，而它的注册表定义里写着
  // connectAction: "terminal" —— 定义与行为对不上。现在只有注册表一条来源。
  it("opens the cluster page for a k8s asset, from the registry alone", async () => {
    await openAssetConnection(makeAsset(4, "prod", "k8s"));

    expect(openTab).toHaveBeenCalledWith({
      id: "k8s-4",
      type: "page",
      label: "prod",
      icon: "kubernetes",
      meta: { type: "page", pageId: "k8s-cluster", assetId: 4 },
    });
    expect(connect).not.toHaveBeenCalled();
  });

  it("opens an extension's connect page for its asset type", async () => {
    registerExtensionAssetTypes("acme", {
      name: "acme",
      version: "1.0.0",
      icon: "cloud",
      i18n: { displayName: "Acme", description: "" },
      assetTypes: [{ type: "acme-store", i18n: { name: "Acme" } }],
      frontend: {
        entry: "index.js",
        styles: "",
        pages: [{ id: "browser", slot: "asset.connect", i18n: { name: "Browser" }, component: "Browser" }],
      },
    } as ExtManifest);

    await openAssetConnection(makeAsset(5, "bucket", "acme-store"));

    expect(openTab).toHaveBeenCalledWith({
      id: "ext-acme-browser-5",
      type: "page",
      label: "bucket",
      icon: "cloud",
      meta: { type: "page", pageId: "browser", assetId: 5, extensionName: "acme" },
    });
    unregisterExtensionAssetTypes("acme");
  });

  it("does nothing for a type no extension provides (uninstalled extension asset)", async () => {
    await openAssetConnection(makeAsset(6, "orphan", "gone-type"));
    expect(openTab).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
    expect(openQueryTab).not.toHaveBeenCalled();
  });

  it.each(["failed", "cancelled", "disconnected"] as const)(
    "activates and directly reconnects an existing tab in %s phase",
    async (phase: TabConnectionPhase) => {
      const tab: Tab = {
        id: "ssh-tab-1",
        type: "terminal",
        label: "web",
        meta: { type: "terminal", assetId: 2, assetName: "web", assetIcon: "", host: "h", port: 22, username: "root" },
      };
      mockTabs = [tab];
      const reconnect = vi.fn();
      cleanups.push(registerLiveTabReconnector(tab.id, { getPhase: () => phase, reconnect }));

      await openAssetConnection(makeAsset(2, "web", "ssh"));

      expect(activateTab).toHaveBeenCalledWith("ssh-tab-1");
      expect(reconnect).toHaveBeenCalledTimes(1);
      expect(connect).not.toHaveBeenCalled();
    }
  );

  it.each(["connecting", "waiting_input", "connected"] as const)(
    "only focuses an existing tab in %s phase without interrupting it",
    async (phase: TabConnectionPhase) => {
      const tab: Tab = {
        id: "ssh-tab-2",
        type: "terminal",
        label: "web",
        meta: { type: "terminal", assetId: 2, assetName: "web", assetIcon: "", host: "h", port: 22, username: "root" },
      };
      mockTabs = [tab];
      const reconnect = vi.fn();
      cleanups.push(registerLiveTabReconnector(tab.id, { getPhase: () => phase, reconnect }));

      await openAssetConnection(makeAsset(2, "web", "ssh"));

      expect(activateTab).toHaveBeenCalledWith("ssh-tab-2");
      expect(reconnect).not.toHaveBeenCalled();
      expect(connect).not.toHaveBeenCalled();
    }
  );

  it("matches multi-tab VNC pages by meta (vnc-${id}-${ts}) and respects multi-tab priority", async () => {
    const failedTab: Tab = {
      id: "vnc-9-1700000001",
      type: "page",
      label: "vnc",
      meta: { type: "page", pageId: "vnc", assetId: 9 },
    };
    const healthyTab: Tab = {
      id: "vnc-9-1700000002",
      type: "page",
      label: "vnc",
      meta: { type: "page", pageId: "vnc", assetId: 9 },
    };
    mockTabs = [failedTab, healthyTab];
    const reconnectFailed = vi.fn();
    const reconnectHealthy = vi.fn();
    cleanups.push(
      registerLiveTabReconnector(failedTab.id, { getPhase: () => "failed", reconnect: reconnectFailed }),
      registerLiveTabReconnector(healthyTab.id, { getPhase: () => "connected", reconnect: reconnectHealthy })
    );

    const vncAsset = makeAsset(9, "vnc", "vnc");
    expect(findExistingAssetTabs(vncAsset, mockTabs).map((t) => t.id)).toEqual([failedTab.id, healthyTab.id]);

    // When neither is active, healthy tab is focused without interrupting or reconnecting
    await openAssetConnection(vncAsset);
    expect(activateTab).toHaveBeenLastCalledWith(healthyTab.id);
    expect(reconnectFailed).not.toHaveBeenCalled();
    expect(reconnectHealthy).not.toHaveBeenCalled();

    // When the failed tab is currently active, double-clicking reconnects that active tab
    mockActiveTabId = failedTab.id;
    await openAssetConnection(vncAsset);
    expect(activateTab).toHaveBeenLastCalledWith(failedTab.id);
    expect(reconnectFailed).toHaveBeenCalledTimes(1);
    expect(openTab).not.toHaveBeenCalled();
  });

  it("reconnectAssetConnection forces reconnect even while connecting, and opens a tab if none exists", async () => {
    const tab: Tab = {
      id: "rdp-3",
      type: "page",
      label: "desktop",
      meta: { type: "page", pageId: "rdp", assetId: 3 },
    };
    mockTabs = [tab];
    const reconnect = vi.fn();
    cleanups.push(registerLiveTabReconnector(tab.id, { getPhase: () => "connecting", reconnect }));

    await reconnectAssetConnection(makeAsset(3, "desktop", "rdp"));
    expect(activateTab).toHaveBeenCalledWith("rdp-3");
    expect(reconnect).toHaveBeenCalledTimes(1);

    mockTabs = [];
    await reconnectAssetConnection(makeAsset(3, "desktop", "rdp"));
    expect(openTab).toHaveBeenCalledTimes(1);
  });
});
