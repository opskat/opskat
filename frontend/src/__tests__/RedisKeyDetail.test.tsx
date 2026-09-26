import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { toast } from "sonner";
import type { redis_svc } from "../../wailsjs/go/models";
import { RedisKeyDetail } from "../components/query/RedisKeyDetail";
import { RedisStreamViewer } from "../components/query/RedisStreamViewer";
import { RedisCollectionTable } from "../components/query/RedisCollectionTable";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { ExecuteRedisArgs } from "../../wailsjs/go/query/Query";
import { RedisDeleteKeys, RedisGetKeyDetail, RedisSetKeyTTL, RedisStreamAdd } from "../../wailsjs/go/redis/Redis";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

describe("RedisKeyDetail", () => {
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
          currentDb: 2,
          keys: ["user:1"],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "0",
          hasMore: false,
          selectedKey: "user:1",
          keyInfo: {
            type: "string",
            ttl: 60,
            size: 8,
            total: -1,
            value: "value",
            valueCursor: "",
            valueOffset: 0,
            hasMoreValues: false,
            loadingMore: false,
          },
          dbKeyCounts: {},
          error: null,
        },
      },
    });
    vi.mocked(RedisGetKeyDetail).mockResolvedValue({
      key: "user:1",
      type: "string",
      ttl: 120,
      size: 8,
      total: -1,
      value: "value",
      valueCursor: "",
      valueOffset: 0,
      hasMoreValues: false,
    });
  });

  it("sets ttl through the typed redis binding", async () => {
    vi.mocked(RedisSetKeyTTL).mockResolvedValue(undefined);

    render(<RedisKeyDetail tabId="query-10" />);

    fireEvent.click(screen.getByText(/query.ttl:/));
    fireEvent.change(screen.getByPlaceholderText("query.ttlInput"), { target: { value: "120" } });
    fireEvent.click(screen.getByText("query.setTtl"));

    await waitFor(() => {
      expect(RedisSetKeyTTL).toHaveBeenCalledWith(10, 2, "user:1", 120);
    });
    expect(ExecuteRedisArgs).not.toHaveBeenCalled();
  });

  it("reports a failed key and does not evict it from the store when delete resolves with a partial failure", async () => {
    // RedisDeleteKeys resolves even when a key failed to delete (e.g. a cluster CLUSTERDOWN on
    // its slot) — it does not reject, so the UI must read the failure from the result.
    vi.mocked(RedisDeleteKeys).mockResolvedValue({
      deleted: 0,
      failed: [{ key: "user:1", error: "CLUSTERDOWN" }],
    } as unknown as redis_svc.RedisDeleteResult);

    render(<RedisKeyDetail tabId="query-10" />);
    fireEvent.click(screen.getByTitle("query.deleteKey"));
    fireEvent.click(screen.getByRole("button", { name: "action.delete" }));

    await waitFor(() => {
      // Redis's own error (e.g. CLUSTERDOWN) must reach the user verbatim, per failed key.
      expect(toast.error).toHaveBeenCalledWith("query.redisDeleteKeysFailed", {
        description: "user:1: CLUSTERDOWN",
      });
    });
    expect(useQueryStore.getState().redisStates["query-10"].selectedKey).toBe("user:1");
  });

  it("clears the selection when the key no longer exists (DEL returned 0, nothing failed)", async () => {
    vi.mocked(RedisDeleteKeys).mockResolvedValue({ deleted: 0, failed: [] } as unknown as redis_svc.RedisDeleteResult);

    render(<RedisKeyDetail tabId="query-10" />);
    fireEvent.click(screen.getByTitle("query.deleteKey"));
    fireEvent.click(screen.getByRole("button", { name: "action.delete" }));

    await waitFor(() => {
      expect(useQueryStore.getState().redisStates["query-10"].selectedKey).not.toBe("user:1");
    });
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("highlights JSON string values without wrapping long content out of the detail area", () => {
    useQueryStore.setState((s) => ({
      redisStates: {
        ...s.redisStates,
        "query-10": {
          ...s.redisStates["query-10"],
          selectedKey: "json:1",
          keyInfo: {
            type: "string",
            ttl: -1,
            size: 34,
            total: -1,
            value: '{"a":1,"enabled":true,"payload":"x"}',
            valueCursor: "",
            valueOffset: 0,
            hasMoreValues: false,
            loadingMore: false,
          },
        },
      },
    }));

    render(<RedisKeyDetail tabId="query-10" />);

    const valueBox = screen.getByTestId("redis-string-value");
    expect(valueBox).toHaveClass("overflow-auto", "whitespace-pre", "select-text");
    expect(valueBox).not.toHaveClass("break-all");
    expect(within(valueBox).getByText('"a"')).toHaveClass("text-syntax-string");
    expect(within(valueBox).getByText("1")).toHaveClass("text-syntax-number");
    expect(within(valueBox).getByText("true")).toHaveClass("text-syntax-boolean");
  });

  it("renders the same value as raw / hex / base64 when switching view modes", () => {
    useQueryStore.setState((s) => ({
      redisStates: {
        ...s.redisStates,
        "query-10": {
          ...s.redisStates["query-10"],
          selectedKey: "bin:1",
          keyInfo: {
            type: "string",
            ttl: -1,
            size: 5,
            total: -1,
            value: "hi 中",
            valueCursor: "",
            valueOffset: 0,
            hasMoreValues: false,
            loadingMore: false,
          },
        },
      },
    }));

    render(<RedisKeyDetail tabId="query-10" />);

    expect(screen.getByTestId("redis-string-value").textContent).toBe("hi 中");

    fireEvent.click(screen.getByText("Hex"));
    expect(screen.getByTestId("redis-string-value").textContent).toBe("686920e4b8ad");

    fireEvent.click(screen.getByText("Base64"));
    expect(screen.getByTestId("redis-string-value").textContent).toBe("aGkg5Lit");

    fireEvent.click(screen.getByText("query.rawText"));
    expect(screen.getByTestId("redis-string-value").textContent).toBe("hi 中");
  });

  it("executes command input with quoted arguments preserved", async () => {
    vi.mocked(ExecuteRedisArgs).mockResolvedValue(JSON.stringify({ type: "string", value: "OK" }));

    render(<RedisKeyDetail tabId="query-10" />);

    fireEvent.change(screen.getByPlaceholderText("query.redisPlaceholder"), {
      target: { value: 'SET "my key" "hello world"' },
    });
    fireEvent.keyDown(screen.getByPlaceholderText("query.redisPlaceholder"), { key: "Enter" });

    await waitFor(() => {
      expect(ExecuteRedisArgs).toHaveBeenCalledWith(10, ["SET", "my key", "hello world"], "2");
    });
  });
});

describe("RedisKeyDetail cluster mode", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTabStore.setState({
      activeTabId: "query-20",
      tabs: [
        {
          id: "query-20",
          type: "query",
          label: "Redis Cluster",
          meta: {
            type: "query",
            assetId: 20,
            assetName: "Redis Cluster",
            assetIcon: "",
            assetType: "redis",
            redisMode: "cluster",
          },
        },
      ],
    });
    useQueryStore.setState({
      redisStates: {
        "query-20": {
          currentDb: 0,
          keys: ["session:{u1}:profile"],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "0",
          hasMore: false,
          selectedKey: "session:{u1}:profile",
          keyInfo: {
            type: "hash",
            ttl: -1,
            size: 2,
            total: 2,
            value: [
              ["name", "alice"],
              ["role", "admin"],
            ],
            valueCursor: "",
            valueOffset: 0,
            hasMoreValues: false,
            loadingMore: false,
            slot: 8723,
            node: "10.20.0.12:6379",
          },
          dbKeyCounts: {},
          error: null,
          scanNode: "",
        },
      },
    });
  });

  it("shows a slot · node tag in the key header", () => {
    render(<RedisKeyDetail tabId="query-20" />);

    const tag = screen.getByTestId("redis-key-slot-tag");
    expect(tag).toHaveAttribute("data-slot", "8723");
    expect(tag).toHaveAttribute("data-node", "10.20.0.12:6379");
  });

  it("sends the footer-selected master as scope for a console command", async () => {
    useQueryStore.setState((s) => ({
      redisStates: {
        ...s.redisStates,
        "query-20": { ...s.redisStates["query-20"], scanNode: "10.20.0.11:6379" },
      },
    }));
    vi.mocked(ExecuteRedisArgs).mockResolvedValue(JSON.stringify({ type: "string", value: "OK" }));

    render(<RedisKeyDetail tabId="query-20" />);
    fireEvent.change(screen.getByPlaceholderText("query.redisPlaceholder"), { target: { value: "DBSIZE" } });
    fireEvent.keyDown(screen.getByPlaceholderText("query.redisPlaceholder"), { key: "Enter" });

    await waitFor(() => {
      expect(ExecuteRedisArgs).toHaveBeenCalledWith(20, ["DBSIZE"], "10.20.0.11:6379");
    });
  });

  it("shows a hint to pick a node when the backend reports a node is required and all masters are selected", async () => {
    vi.mocked(ExecuteRedisArgs).mockRejectedValue(
      new Error(
        "redis cluster: DBSIZE has no key, so it must run on a specific node — pass scope as one of the master addresses: 10.20.0.11:6379, 10.20.0.12:6379"
      )
    );

    render(<RedisKeyDetail tabId="query-20" />);
    fireEvent.change(screen.getByPlaceholderText("query.redisPlaceholder"), { target: { value: "DBSIZE" } });
    fireEvent.keyDown(screen.getByPlaceholderText("query.redisPlaceholder"), { key: "Enter" });

    await waitFor(() => {
      expect(ExecuteRedisArgs).toHaveBeenCalledWith(20, ["DBSIZE"], "");
    });
    expect(await screen.findByText(/query.redisSelectNodeHint/)).toBeInTheDocument();
  });
});

describe("RedisStreamViewer", () => {
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
          currentDb: 1,
          keys: ["events"],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "0",
          hasMore: false,
          selectedKey: "events",
          keyInfo: null,
          dbKeyCounts: {},
          error: null,
        },
      },
    });
    vi.mocked(RedisGetKeyDetail).mockResolvedValue({
      key: "events",
      type: "stream",
      ttl: -1,
      size: 0,
      total: 1,
      value: [{ id: "1-0", fields: { name: "Ada" } }],
      valueCursor: "1-0",
      valueOffset: 1,
      hasMoreValues: false,
    });
  });

  it("adds stream entries through the typed redis binding", async () => {
    vi.mocked(RedisStreamAdd).mockResolvedValue(undefined);

    render(
      <RedisStreamViewer
        tabId="query-10"
        t={(key) => key}
        info={{
          type: "stream",
          ttl: -1,
          size: 0,
          total: 0,
          value: [],
          valueCursor: "",
          valueOffset: 0,
          hasMoreValues: false,
          loadingMore: false,
        }}
      />
    );

    fireEvent.change(screen.getByPlaceholderText("query.streamEntryId"), { target: { value: "*" } });
    fireEvent.change(screen.getByPlaceholderText("query.streamField"), { target: { value: "name" } });
    fireEvent.change(screen.getByPlaceholderText("query.streamValue"), { target: { value: "Ada" } });
    fireEvent.click(screen.getByTitle("query.addEntry"));

    await waitFor(() => {
      expect(RedisStreamAdd).toHaveBeenCalledWith(10, 1, "events", "*", [{ field: "name", value: "Ada" }]);
    });
  });

  it("shows loaded count and delegates loading more values to the query store", () => {
    const loadMoreValues = vi.fn();
    useQueryStore.setState({ loadMoreValues });

    render(
      <RedisStreamViewer
        tabId="query-10"
        t={(key, opts) => (key === "query.loadedOfTotal" ? `${opts?.loaded}/${opts?.total}` : key)}
        info={{
          type: "stream",
          ttl: -1,
          size: 0,
          total: 4,
          value: [{ id: "1-0", fields: { name: "Ada" } }],
          valueCursor: "1-0",
          valueOffset: 1,
          hasMoreValues: true,
          loadingMore: false,
        }}
      />
    );

    expect(screen.getByText("1/4")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "query.loadMore" }));
    expect(loadMoreValues).toHaveBeenCalledWith("query-10");
  });

  it("disables the load more action while more stream values are loading", () => {
    render(
      <RedisStreamViewer
        tabId="query-10"
        t={(key) => key}
        info={{
          type: "stream",
          ttl: -1,
          size: 0,
          total: -1,
          value: [],
          valueCursor: "",
          valueOffset: 0,
          hasMoreValues: true,
          loadingMore: true,
        }}
      />
    );

    expect(screen.getByRole("button", { name: "query.loadMore" })).toBeDisabled();
  });

  it("lets users select the expanded entry JSON while preserving clickable list rows", () => {
    render(
      <RedisStreamViewer
        tabId="query-10"
        t={(key) => key}
        info={{
          type: "stream",
          ttl: -1,
          size: 0,
          total: 1,
          value: [{ id: "1-0", fields: { name: "Ada" } }],
          valueCursor: "1-0",
          valueOffset: 1,
          hasMoreValues: false,
          loadingMore: false,
        }}
      />
    );

    fireEvent.click(screen.getByText("1-0"));
    const json = screen.getAllByText(/"name": "Ada"/).find((node) => node.tagName === "PRE");
    expect(json).toHaveClass("select-text");
    expect(screen.getByText("1-0").parentElement).not.toHaveClass("select-text");
  });
});

describe("RedisCollectionTable", () => {
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
          currentDb: 1,
          keys: ["members"],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "0",
          hasMore: false,
          selectedKey: "members",
          keyInfo: null,
          dbKeyCounts: {},
          error: null,
        },
      },
    });
  });

  it("shows loaded count and delegates loading more collection values to the query store", () => {
    const loadMoreValues = vi.fn();
    useQueryStore.setState({ loadMoreValues });

    render(
      <RedisCollectionTable
        tabId="query-10"
        t={(key, opts) => (key === "query.loadedOfTotal" ? `${opts?.loaded}/${opts?.total}` : key)}
        info={{
          type: "set",
          ttl: -1,
          size: 0,
          total: 5,
          value: ["one", "two"],
          valueCursor: "2",
          valueOffset: 2,
          hasMoreValues: true,
          loadingMore: false,
        }}
      />
    );

    expect(screen.getByText("2/5")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "query.loadMore" }));
    expect(loadMoreValues).toHaveBeenCalledWith("query-10");
  });
});
