import { describe, it, expect, beforeEach, vi } from "vitest";
import { ListDatabases, ListMongoDatabases } from "../../../wailsjs/go/query/Query";
import { RedisListDatabases, RedisScanKeys } from "../../../wailsjs/go/redis/Redis";
import { KafkaClusterOverview } from "../../../wailsjs/go/kafka/Kafka";
import { OSSListBuckets } from "../../../wailsjs/go/oss/OSS";
import { asset_entity } from "../../../wailsjs/go/models";
import { useTabStore, type QueryTabMeta, type Tab } from "../tabStore";
import { useAssetStore } from "../assetStore";
import { useQueryStore } from "../queryStore";
import { useKafkaStore } from "../kafkaStore";
import { useOssBrowserStore } from "../ossBrowserStore";
import { getTabConnectionPhase, reconnectTab, reconnectTabIfNeeded } from "@/lib/tabReconnect";

function queryTab(assetId: number, assetType: QueryTabMeta["assetType"]): Tab {
  return {
    id: `query-${assetId}`,
    type: "query",
    label: `${assetType}-${assetId}`,
    meta: {
      type: "query",
      assetId,
      assetName: `${assetType}-${assetId}`,
      assetIcon: "",
      assetType,
    },
  };
}

describe("query / kafka / oss tabReconnect adapters", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAssetStore.setState({ assets: [] });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    useKafkaStore.setState({ states: {} });
    useOssBrowserStore.setState({ tabs: {} });
  });

  it("database: failed initial load reconnects; transient refresh error with loaded databases stays connected", async () => {
    const asset = new asset_entity.Asset({ ID: 11, Name: "pg", Type: "database", Config: '{"driver":"postgresql"}' });
    useQueryStore.getState().openQueryTab(asset);
    const tab = useTabStore.getState().tabs[0];

    // Initial load failure -> failed -> reconnectTabIfNeeded retries loadDatabases
    vi.mocked(ListDatabases).mockRejectedValueOnce(new Error("dial timeout"));
    await useQueryStore.getState().loadDatabases(tab.id);
    expect(getTabConnectionPhase(tab)).toBe("failed");

    vi.mocked(ListDatabases).mockResolvedValueOnce(["app", "postgres"] as never);
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(getTabConnectionPhase(tab)).toBe("connected");
    expect(useQueryStore.getState().dbStates[tab.id].databases).toEqual(["app", "postgres"]);

    // Transient refresh error while databases are already loaded -> stays connected (no auto-reconnect)
    vi.mocked(ListDatabases).mockRejectedValueOnce(new Error("temporary blip"));
    await useQueryStore.getState().loadDatabases(tab.id);
    expect(getTabConnectionPhase(tab)).toBe("connected");
    vi.mocked(ListDatabases).mockClear();
    expect(await reconnectTabIfNeeded(tab)).toBe(false);
    expect(ListDatabases).not.toHaveBeenCalled();

    // Explicit reconnectTab reloads even when connected
    vi.mocked(ListDatabases).mockResolvedValueOnce(["app", "postgres"] as never);
    expect(await reconnectTab(tab)).toBe(true);
    expect(ListDatabases).toHaveBeenCalledTimes(1);
  });

  it("redis: failed initial scan reconnects; loaded keys with refresh error stays connected", async () => {
    const asset = new asset_entity.Asset({ ID: 12, Name: "cache", Type: "redis", Config: "{}" });
    useQueryStore.getState().openQueryTab(asset);
    const tab = useTabStore.getState().tabs[0];

    vi.mocked(RedisScanKeys).mockRejectedValueOnce(new Error("connection refused"));
    await useQueryStore.getState().scanKeys(tab.id, true);
    expect(getTabConnectionPhase(tab)).toBe("failed");

    vi.mocked(RedisScanKeys).mockResolvedValueOnce(
      JSON.stringify({ keys: ["k1"], cursor: "0", hasMore: false }) as never
    );
    vi.mocked(RedisListDatabases).mockResolvedValueOnce(JSON.stringify({ 0: 1 }) as never);
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(getTabConnectionPhase(tab)).toBe("connected");

    // Transient error after keys loaded -> stays connected
    vi.mocked(RedisScanKeys).mockRejectedValueOnce(new Error("timeout"));
    await useQueryStore.getState().scanKeys(tab.id, false);
    expect(getTabConnectionPhase(tab)).toBe("connected");
    vi.mocked(RedisScanKeys).mockClear();
    expect(await reconnectTabIfNeeded(tab)).toBe(false);
    expect(RedisScanKeys).not.toHaveBeenCalled();
  });

  it("mongodb: exposes loadingDbs as connecting, reconnects on initial error, and keeps loaded data on transient error", async () => {
    const asset = new asset_entity.Asset({ ID: 13, Name: "mongo", Type: "mongodb", Config: "{}" });
    useQueryStore.getState().openQueryTab(asset);
    const tab = useTabStore.getState().tabs[0];

    let rejectFirst: (err: Error) => void = () => {};
    vi.mocked(ListMongoDatabases).mockReturnValueOnce(new Promise((_, rej) => (rejectFirst = rej)) as never);
    const pending = useQueryStore.getState().loadMongoDatabases(tab.id);
    expect(useQueryStore.getState().mongoStates[tab.id].loadingDbs).toBe(true);
    expect(getTabConnectionPhase(tab)).toBe("connecting");

    rejectFirst(new Error("auth failed"));
    await pending;
    expect(useQueryStore.getState().mongoStates[tab.id].loadingDbs).toBe(false);
    expect(getTabConnectionPhase(tab)).toBe("failed");

    vi.mocked(ListMongoDatabases).mockResolvedValueOnce(JSON.stringify(["admin", "shop"]) as never);
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(getTabConnectionPhase(tab)).toBe("connected");

    // Transient error with loaded databases -> stays connected
    vi.mocked(ListMongoDatabases).mockRejectedValueOnce(new Error("network glitch"));
    await useQueryStore.getState().loadMongoDatabases(tab.id);
    expect(getTabConnectionPhase(tab)).toBe("connected");
  });

  it("kafka: reports connecting / failed / connected and refreshes active view on reconnect", async () => {
    const tab = queryTab(14, "kafka");
    useTabStore.setState({ tabs: [tab], activeTabId: tab.id });
    useKafkaStore.getState().ensureTab(tab.id);

    let rejectOverview: (err: Error) => void = () => {};
    vi.mocked(KafkaClusterOverview).mockReturnValueOnce(new Promise((_, rej) => (rejectOverview = rej)) as never);
    const pending = useKafkaStore.getState().loadOverview(tab.id);
    expect(getTabConnectionPhase(tab)).toBe("connecting");

    rejectOverview(new Error("broker unavailable"));
    await pending;
    expect(getTabConnectionPhase(tab)).toBe("failed");

    vi.mocked(KafkaClusterOverview).mockResolvedValueOnce({
      clusterId: "c1",
      controllerId: 1,
      brokerCount: 3,
      topicCount: 10,
      internalTopicCount: 1,
      totalPartitions: 30,
      underReplicatedPartitions: 0,
      offlinePartitions: 0,
      consumerGroupCount: 2,
    } as never);
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(getTabConnectionPhase(tab)).toBe("connected");
  });

  it("oss: reports connecting / failed / connected and reloads buckets on reconnect", async () => {
    const tab = queryTab(15, "oss");
    useTabStore.setState({ tabs: [tab], activeTabId: tab.id });
    useAssetStore.setState({
      assets: [new asset_entity.Asset({ ID: 15, Name: "s3", Type: "oss", Config: '{"bucket":"logs"}' })],
    });

    let rejectBuckets: (err: Error) => void = () => {};
    vi.mocked(OSSListBuckets).mockReturnValueOnce(new Promise((_, rej) => (rejectBuckets = rej)) as never);
    const pending = useOssBrowserStore.getState().loadBuckets(tab.id, 15, "logs");
    expect(getTabConnectionPhase(tab)).toBe("connecting");

    rejectBuckets(new Error("access denied"));
    await expect(pending).rejects.toThrow("access denied");
    expect(getTabConnectionPhase(tab)).toBe("failed");

    vi.mocked(OSSListBuckets).mockResolvedValueOnce([{ name: "logs", creationDate: "" }] as never);
    expect(await reconnectTabIfNeeded(tab)).toBe(true);
    expect(getTabConnectionPhase(tab)).toBe("connected");
  });
});
