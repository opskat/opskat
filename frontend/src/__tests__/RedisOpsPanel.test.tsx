import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from "vitest";
import { RedisOpsPanel } from "../components/query/RedisOpsPanel";
import { useTabStore } from "../stores/tabStore";
import { useQueryStore } from "../stores/queryStore";
import { ExecuteRedis } from "../../wailsjs/go/query/Query";
import { RedisClusterOverview, RedisSentinelOverview } from "../../wailsjs/go/redis/Redis";
import { redis_svc } from "../../wailsjs/go/models";

// Radix menus need these DOM APIs happy-dom doesn't implement (see VNCToolbar.test.tsx).
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn(() => false);
  Element.prototype.releasePointerCapture = vi.fn();
});

describe("RedisOpsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTabStore.setState({
      activeTabId: "query-10",
      tabs: [
        {
          id: "query-10",
          type: "query",
          label: "Redis",
          meta: { type: "query", assetId: 10, assetName: "Redis", assetIcon: "", assetType: "redis" },
        },
      ],
    });
    useQueryStore.setState({
      redisStates: {
        "query-10": {
          currentDb: 0,
          keys: [],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "0",
          hasMore: false,
          selectedKey: null,
          keyInfo: null,
          dbKeyCounts: {},
          error: null,
        },
      },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders redis info details, keyspace stats, and searchable info rows", async () => {
    vi.mocked(ExecuteRedis).mockResolvedValue(
      JSON.stringify({
        type: "string",
        value:
          "# Server\r\nredis_version:7.4.8\r\nos:Linux6.8.7.2-microsoft-standard-WSL2x86_64\r\nprocess_id:1\r\nredis_git_sha1:00000000\r\nredis_build_id:dc3fdca8addf42ba\r\n# Clients\r\nconnected_clients:12\r\ntotal_connections_received:6684\r\n# Memory\r\nused_memory_human:79.33M\r\nused_memory_peak_human:83.89M\r\nused_memory_lua_human:43K\r\n# Stats\r\ntotal_commands_processed:32863384\r\n# Keyspace\r\ndb0:keys=8795,expires=8771,avg_ttl=253391607\r\n",
      })
    );

    render(<RedisOpsPanel tabId="query-10" />);

    await waitFor(() => {
      expect(ExecuteRedis).toHaveBeenCalledWith(10, "INFO", "0");
    });
    expect(screen.getByText("query.redisServer")).toBeInTheDocument();
    expect(screen.getByText("query.redisVersion:")).toBeInTheDocument();
    expect(screen.getAllByText("7.4.8").length).toBeGreaterThan(0);
    expect(screen.getAllByText("db0").length).toBeGreaterThan(0);
    expect(screen.getByText("8,795")).toBeInTheDocument();
    expect(screen.getByText("253,391,607")).toBeInTheDocument();
    expect(screen.getByText("redis_build_id")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("query.redisInfoSearch"), { target: { value: "git" } });

    expect(screen.getByText("redis_git_sha1")).toBeInTheDocument();
    expect(screen.queryByText("redis_build_id")).not.toBeInTheDocument();
    expect(screen.queryByText("process_id")).not.toBeInTheDocument();
  });

  it("refreshes every two seconds while auto refresh is enabled", async () => {
    vi.useFakeTimers();
    vi.mocked(ExecuteRedis).mockResolvedValue(
      JSON.stringify({
        type: "string",
        value: "# Server\r\nredis_version:7.4.8\r\n",
      })
    );

    render(<RedisOpsPanel tabId="query-10" />);

    await act(async () => {
      await Promise.resolve();
    });
    expect(ExecuteRedis).toHaveBeenCalledTimes(1);
    vi.mocked(ExecuteRedis).mockClear();

    fireEvent.click(screen.getByRole("switch"));
    await vi.advanceTimersByTimeAsync(1_999);
    expect(ExecuteRedis).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    expect(ExecuteRedis).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(2_000);
    expect(ExecuteRedis).toHaveBeenCalledTimes(2);
  });

  describe("cluster mode", () => {
    beforeEach(() => {
      useTabStore.setState({
        activeTabId: "query-10",
        tabs: [
          {
            id: "query-10",
            type: "query",
            label: "Redis",
            meta: {
              type: "query",
              assetId: 10,
              assetName: "Redis",
              assetIcon: "",
              assetType: "redis",
              redisMode: "cluster",
            },
          },
        ],
      });
    });

    function clusterOverview(overrides: Partial<redis_svc.RedisClusterOverview> = {}) {
      return new redis_svc.RedisClusterOverview({
        state: "fail",
        slotsAssigned: 16384,
        slotsOk: 10923,
        slotsPfail: 0,
        slotsFail: 5461,
        totalKeys: 131,
        keysPartial: true,
        masterCount: 2,
        replicaCount: 1,
        infoNode: "10.20.0.11:6379",
        info: "# Server\r\nredis_version:7.2.5\r\n",
        masters: [
          {
            id: "a3250d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            addr: "10.20.0.11:6379",
            role: "master",
            masterId: "",
            slots: "0-5460",
            slotCount: 5461,
            flags: ["master"],
            linkState: "connected",
            status: "ok",
            reachable: true,
            keys: 62,
            usedMemory: 2528000,
            usedMemoryHuman: "2.41M",
            opsPerSec: 118,
            replicas: [
              {
                id: "e41766c8bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                addr: "10.20.0.14:6379",
                role: "replica",
                masterId: "a3250d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                slots: "",
                slotCount: 0,
                flags: ["slave"],
                linkState: "connected",
                status: "ok",
                reachable: true,
                keys: 62,
                usedMemory: 2495000,
                usedMemoryHuman: "2.38M",
                opsPerSec: 3,
              },
            ],
          },
          {
            id: "1fda5dd1ccccccccccccccccccccccccccccccccc",
            addr: "10.20.0.13:6379",
            role: "master",
            masterId: "",
            slots: "10923-16383",
            slotCount: 5461,
            flags: ["master", "fail"],
            linkState: "disconnected",
            status: "fail",
            error: "连接超时",
            reachable: false,
            keys: -1,
            usedMemory: -1,
            usedMemoryHuman: "",
            opsPerSec: -1,
          },
        ],
        ...overrides,
      });
    }

    it("renders cluster summary cards, node table and the fail top bar", async () => {
      vi.mocked(RedisClusterOverview).mockResolvedValue(clusterOverview());

      render(<RedisOpsPanel tabId="query-10" />);

      await waitFor(() => {
        expect(RedisClusterOverview).toHaveBeenCalledWith(10, "");
      });

      // Top bar: cluster_state fail + unavailable slot count (16384 - 10923 = 5461)
      expect(screen.getByTestId("redis-cluster-state-banner")).toHaveAttribute("data-unavailable-slots", "5461");

      // Summary cards
      const summary = screen.getByTestId("redis-cluster-summary");
      expect(screen.getByTestId("redis-cluster-summary-state")).toHaveTextContent("fail");
      expect(screen.getByTestId("redis-cluster-summary-topology")).toHaveTextContent("2 / 1");
      expect(summary).toHaveTextContent("10923");
      expect(summary).toHaveTextContent("16384");
      expect(summary).toHaveTextContent("≥");
      expect(summary).toHaveTextContent("131");

      // Node table: master + indented replica + failed master row
      const rows = screen.getAllByTestId("redis-cluster-node-row");
      expect(rows).toHaveLength(3);
      expect(rows[0]).toHaveAttribute("data-addr", "10.20.0.11:6379");
      expect(rows[0]).toHaveAttribute("data-role", "master");
      expect(rows[1]).toHaveAttribute("data-addr", "10.20.0.14:6379");
      expect(rows[1]).toHaveAttribute("data-role", "replica");
      expect(rows[2]).toHaveAttribute("data-addr", "10.20.0.13:6379");
      expect(rows[2]).toHaveAttribute("data-status", "fail");
      expect(rows[2]).toHaveTextContent("连接超时");
      // Unreachable node's key/memory/ops columns show "—", not the raw -1 sentinel
      const unreachableCells = rows[2].querySelectorAll("td");
      expect(unreachableCells[3]).toHaveTextContent("—");
      expect(unreachableCells[4]).toHaveTextContent("—");
      expect(unreachableCells[5]).toHaveTextContent("—");

      // Server/memory/runtime panels parsed from overview.info
      expect(screen.getAllByText("7.2.5").length).toBeGreaterThan(0);
    });

    it("shows the backend's master/replica count, not masters.length, after a failover leaves a failed master without slots", async () => {
      // E20: after a failover, 10.20.0.13:6379 lost its slots but the node table still lists
      // it (master,fail, no slots) for troubleshooting. The backend excludes it from
      // masterCount/replicaCount (redis_svc.clusterNodeInfo.isCountedMaster); the summary
      // card must read those fields instead of deriving "4 / 2" from masters.length.
      vi.mocked(RedisClusterOverview).mockResolvedValue(
        clusterOverview({
          masterCount: 3,
          replicaCount: 2,
          masters: [
            {
              id: "a3250d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
              addr: "10.20.0.11:6379",
              role: "master",
              masterId: "",
              slots: "0-5460",
              slotCount: 5461,
              flags: ["master"],
              linkState: "connected",
              status: "ok",
              reachable: true,
              keys: 62,
              usedMemory: 2528000,
              usedMemoryHuman: "2.41M",
              opsPerSec: 118,
              replicas: [
                {
                  id: "e41766c8bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                  addr: "10.20.0.14:6379",
                  role: "replica",
                  masterId: "a3250d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                  slots: "",
                  slotCount: 0,
                  flags: ["slave"],
                  linkState: "connected",
                  status: "ok",
                  reachable: true,
                  keys: 62,
                  usedMemory: 2495000,
                  usedMemoryHuman: "2.38M",
                  opsPerSec: 3,
                },
              ],
            },
            {
              id: "b1234d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
              addr: "10.20.0.12:6379",
              role: "master",
              masterId: "",
              slots: "5461-10922",
              slotCount: 5462,
              flags: ["master"],
              linkState: "connected",
              status: "ok",
              reachable: true,
              keys: 60,
              usedMemory: 2400000,
              usedMemoryHuman: "2.29M",
              opsPerSec: 100,
              replicas: [
                {
                  id: "f22222aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                  addr: "10.20.0.15:6379",
                  role: "replica",
                  masterId: "b1234d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                  slots: "",
                  slotCount: 0,
                  flags: ["slave"],
                  linkState: "connected",
                  status: "ok",
                  reachable: true,
                  keys: 60,
                  usedMemory: 2400000,
                  usedMemoryHuman: "2.29M",
                  opsPerSec: 4,
                },
              ],
            },
            {
              id: "c9999d88aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
              addr: "10.20.0.16:6379",
              role: "master",
              masterId: "",
              slots: "10923-16383",
              slotCount: 5461,
              flags: ["master"],
              linkState: "connected",
              status: "ok",
              reachable: true,
              keys: 58,
              usedMemory: 2300000,
              usedMemoryHuman: "2.19M",
              opsPerSec: 90,
            },
            {
              // Lost its slots after the failover; still master-flagged but excluded from
              // masterCount/replicaCount by the shared Go counting rule.
              id: "1fda5dd1ccccccccccccccccccccccccccccccccc",
              addr: "10.20.0.13:6379",
              role: "master",
              masterId: "",
              slots: "",
              slotCount: 0,
              flags: ["master", "fail"],
              linkState: "disconnected",
              status: "fail",
              error: "node is not part of the cluster",
              reachable: false,
              keys: -1,
              usedMemory: -1,
              usedMemoryHuman: "",
              opsPerSec: -1,
            },
          ] as unknown as redis_svc.RedisClusterNode[],
        })
      );

      render(<RedisOpsPanel tabId="query-10" />);

      await waitFor(() => {
        expect(RedisClusterOverview).toHaveBeenCalledWith(10, "");
      });

      expect(screen.getByTestId("redis-cluster-summary-topology")).toHaveTextContent("3 / 2");
    });

    it("does not show the fail banner when cluster_state is ok, and switching the INFO node refetches", async () => {
      const user = userEvent.setup();
      vi.mocked(RedisClusterOverview).mockResolvedValue(
        clusterOverview({ state: "ok", slotsOk: 16384, slotsFail: 0, keysPartial: false, totalKeys: 205 })
      );

      render(<RedisOpsPanel tabId="query-10" />);

      await waitFor(() => {
        expect(RedisClusterOverview).toHaveBeenCalledWith(10, "");
      });
      expect(screen.queryByTestId("redis-cluster-state-banner")).not.toBeInTheDocument();

      const select = screen.getByTestId("redis-info-node-select");
      expect(select).toHaveTextContent("10.20.0.11:6379");

      vi.mocked(RedisClusterOverview).mockClear();
      await user.click(select);
      const option = await screen.findByTestId("redis-info-node-option-10.20.0.13:6379");
      await user.click(option);

      await waitFor(() => {
        expect(RedisClusterOverview).toHaveBeenCalledWith(10, "10.20.0.13:6379");
      });
    });
  });

  describe("sentinel mode", () => {
    beforeEach(() => {
      useTabStore.setState({
        activeTabId: "query-10",
        tabs: [
          {
            id: "query-10",
            type: "query",
            label: "Redis",
            meta: {
              type: "query",
              assetId: 10,
              assetName: "Redis",
              assetIcon: "",
              assetType: "redis",
              redisMode: "sentinel",
            },
          },
        ],
      });
      vi.mocked(ExecuteRedis).mockResolvedValue(
        JSON.stringify({ type: "string", value: "# Server\r\nredis_version:7.2.5\r\n" })
      );
    });

    it("renders sentinel summary cards, replication topology and sentinel node tables", async () => {
      vi.mocked(RedisSentinelOverview).mockResolvedValue(
        new redis_svc.RedisSentinelOverview({
          masterName: "mymaster",
          master: { addr: "10.20.0.42:6379", flags: "master", status: "ok" },
          quorum: 2,
          replicas: [
            {
              addr: "10.20.0.41:6379",
              flags: "slave",
              status: "ok",
              linkStatus: "ok",
              offset: 100,
              lagSeconds: 0,
              lagBytes: 0,
            },
            {
              addr: "10.20.0.43:6379",
              flags: "slave",
              status: "ok",
              linkStatus: "ok",
              offset: 100,
              lagSeconds: 0,
              lagBytes: 0,
            },
          ],
          sentinels: [
            { addr: "10.20.0.31:26379", flags: "sentinel", status: "ok", queried: true },
            { addr: "10.20.0.32:26379", flags: "sentinel", status: "ok" },
            { addr: "10.20.0.33:26379", flags: "sentinel", status: "ok" },
          ],
        })
      );

      render(<RedisOpsPanel tabId="query-10" />);

      await waitFor(() => {
        expect(RedisSentinelOverview).toHaveBeenCalledWith(10);
      });
      // Existing INFO-derived panels still work (sentinel keeps hitting current master)
      await waitFor(() => {
        expect(ExecuteRedis).toHaveBeenCalledWith(10, "INFO", "0");
      });

      const summary = screen.getByTestId("redis-sentinel-summary");
      expect(summary).toHaveTextContent("mymaster");
      expect(summary).toHaveTextContent("10.20.0.42:6379");
      expect(summary).toHaveTextContent("2 / 3");
      expect(summary).toHaveTextContent("2");

      const topologyRows = screen.getAllByTestId("redis-sentinel-topology-row");
      expect(topologyRows).toHaveLength(3);
      expect(topologyRows[0]).toHaveTextContent("10.20.0.42:6379");
      expect(topologyRows[1]).toHaveTextContent("10.20.0.41:6379");

      const sentinelRows = screen.getAllByTestId("redis-sentinel-node-row");
      expect(sentinelRows).toHaveLength(3);
      expect(sentinelRows[0]).toHaveTextContent("10.20.0.31:26379");
    });
  });
});
