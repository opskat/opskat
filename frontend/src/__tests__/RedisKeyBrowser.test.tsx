import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { toast } from "sonner";
import type { redis_svc } from "../../wailsjs/go/models";
import { RedisKeyBrowser } from "../components/query/RedisKeyBrowser";
import { buildKeyTree, flattenTree, makeLocalKeyMatcher } from "../lib/redisKeyTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { RedisHashSet } from "../../wailsjs/go/redis/Redis";
import {
  RedisDeleteKeys,
  RedisListDatabases,
  RedisListPush,
  RedisScanKeys,
  RedisSetKeyTTL,
  RedisSetStringValue,
} from "../../wailsjs/go/redis/Redis";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

describe("RedisKeyBrowser", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(RedisScanKeys).mockResolvedValue({
      cursor: "0",
      keys: ["common:user:1", "common:user:2", "dispatcher:task:1"],
      hasMore: false,
    });
    vi.mocked(RedisListDatabases).mockResolvedValue([
      { db: 0, keys: 7767, expires: 0, avgTtl: 0 },
      { db: 1, keys: 12, expires: 0, avgTtl: 0 },
    ]);
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
          keys: ["common:user:1", "common:user:2", "dispatcher:task:1"],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "23",
          hasMore: true,
          selectedKey: null,
          keyInfo: null,
          dbKeyCounts: { 0: 7767, 1: 12 },
          error: null,
        },
      },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("defaults to tree view and keeps database selection in the footer", () => {
    render(<RedisKeyBrowser tabId="query-10" />);

    expect(screen.getByTitle("query.listView")).toBeInTheDocument();
    expect(screen.getByTitle("query.createRedisKey")).toBeInTheDocument();
    expect(screen.queryByText("query.loadMore")).not.toBeInTheDocument();
    expect(screen.getByTestId("redis-key-tree")).toHaveAttribute("data-counts-incomplete", "true");
    expect(screen.getByTestId("redis-db-footer")).toHaveTextContent("db0");
  });

  it("creates a string key from the add key dialog", async () => {
    vi.mocked(RedisSetStringValue).mockResolvedValue(undefined);
    vi.mocked(RedisSetKeyTTL).mockResolvedValue(undefined);

    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.click(screen.getByTitle("query.createRedisKey"));
    fireEvent.change(screen.getByTestId("redis-create-key-input"), {
      target: { value: "new:key" },
    });
    fireEvent.change(screen.getByTestId("redis-create-string-value"), {
      target: { value: "hello" },
    });
    fireEvent.change(screen.getByTestId("redis-create-ttl-input"), {
      target: { value: "60" },
    });
    fireEvent.click(screen.getByText("query.createRedisKeySubmit"));

    await waitFor(() => {
      expect(RedisSetStringValue).toHaveBeenCalledWith({
        assetId: 10,
        db: 0,
        key: "new:key",
        value: "hello",
        format: "raw",
      });
    });
    expect(RedisSetKeyTTL).toHaveBeenCalledWith(10, 0, "new:key", 60);
  });

  it("creates a hash key with multiple initial fields", async () => {
    vi.mocked(RedisHashSet).mockResolvedValue(undefined);

    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.click(screen.getByTitle("query.createRedisKey"));
    fireEvent.change(screen.getByTestId("redis-create-key-input"), {
      target: { value: "profile:1" },
    });
    fireEvent.click(screen.getByTestId("redis-create-type-trigger"));
    fireEvent.click(await screen.findByRole("option", { name: "hash" }));
    fireEvent.change(screen.getByTestId("redis-create-hash-field-0"), {
      target: { value: "name" },
    });
    fireEvent.change(screen.getByTestId("redis-create-hash-value-0"), {
      target: { value: "Ada" },
    });
    fireEvent.click(screen.getByTestId("redis-create-add-row"));
    fireEvent.change(screen.getByTestId("redis-create-hash-field-1"), {
      target: { value: "role" },
    });
    fireEvent.change(screen.getByTestId("redis-create-hash-value-1"), {
      target: { value: "admin" },
    });
    fireEvent.click(screen.getByText("query.createRedisKeySubmit"));

    await waitFor(() => {
      expect(RedisHashSet).toHaveBeenCalledTimes(2);
    });
    expect(RedisHashSet).toHaveBeenNthCalledWith(1, 10, 0, "profile:1", "name", "Ada");
    expect(RedisHashSet).toHaveBeenNthCalledWith(2, 10, 0, "profile:1", "role", "admin");
  });

  it("creates a list key in the same order as the initial values", async () => {
    vi.mocked(RedisListPush).mockResolvedValue(undefined);

    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.click(screen.getByTitle("query.createRedisKey"));
    fireEvent.change(screen.getByTestId("redis-create-key-input"), {
      target: { value: "queue:1" },
    });
    fireEvent.click(screen.getByTestId("redis-create-type-trigger"));
    fireEvent.click(await screen.findByRole("option", { name: "list" }));

    fireEvent.change(screen.getAllByPlaceholderText("query.newValue")[0], {
      target: { value: "first" },
    });
    fireEvent.click(screen.getByTestId("redis-create-add-row"));
    fireEvent.change(screen.getAllByPlaceholderText("query.newValue")[1], {
      target: { value: "second" },
    });
    fireEvent.click(screen.getByText("query.createRedisKeySubmit"));

    await waitFor(() => {
      expect(RedisListPush).toHaveBeenCalledTimes(2);
    });
    expect(RedisListPush).toHaveBeenNthCalledWith(1, 10, 0, "queue:1", "first");
    expect(RedisListPush).toHaveBeenNthCalledWith(2, 10, 0, "queue:1", "second");
  });

  it("opens a lightweight database menu and selects a db", async () => {
    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.click(screen.getByRole("button", { name: /db0/ }));

    const menu = screen.getByTestId("redis-db-menu");
    expect(menu).toHaveClass("overflow-y-auto");
    expect(menu).toHaveStyle({ maxHeight: "320px" });

    fireEvent.click(screen.getByRole("option", { name: /^db1\b/ }));

    await waitFor(() => {
      expect(RedisScanKeys).toHaveBeenCalledWith(
        expect.objectContaining({
          assetId: 10,
          db: 1,
          cursor: "0",
        })
      );
    });
    expect(screen.queryByTestId("redis-db-menu")).not.toBeInTheDocument();
  });

  it("includes non-empty databases beyond the default range in the db menu", async () => {
    useQueryStore.setState((s) => ({
      redisStates: {
        ...s.redisStates,
        "query-10": {
          ...s.redisStates["query-10"],
          dbKeyCounts: { 0: 7767, 20: 9 },
        },
      },
    }));

    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.click(screen.getByRole("button", { name: /db0/ }));

    expect(screen.getByRole("option", { name: /^db20\b/ })).toBeInTheDocument();
  });

  it("keeps prefix keys expandable when a key also has children", () => {
    const tree = buildKeyTree(["root", "root:session"], ":");
    const collapsed = flattenTree(tree, new Set(), ":");
    const expanded = flattenTree(tree, new Set(["root"]), ":");

    expect(collapsed[0]).toEqual(
      expect.objectContaining({
        name: "root",
        fullKey: "root",
        hasChildren: true,
        keyCount: 2,
      })
    );
    expect(expanded.map((row) => row.name)).toEqual(["root", "session"]);
  });

  it("opens a prefix key and expands its child keys from tree mode", async () => {
    vi.mocked(RedisScanKeys).mockResolvedValueOnce({
      cursor: "0",
      keys: ["root", "root:session"],
      hasMore: false,
    });

    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.click(await screen.findByRole("button", { name: /^root$/ }));

    await waitFor(() => {
      expect(useQueryStore.getState().redisStates["query-10"].selectedKey).toBe("root");
    });

    fireEvent.click(screen.getByTitle("query.expandFolder root"));

    expect(await screen.findByRole("button", { name: /^session$/ })).toBeInTheDocument();
  });

  it("does not overwrite an existing key from the add key dialog", async () => {
    vi.mocked(RedisSetStringValue).mockResolvedValue(undefined);

    render(<RedisKeyBrowser tabId="query-10" />);
    vi.mocked(RedisScanKeys).mockClear();
    vi.mocked(RedisScanKeys).mockResolvedValueOnce({
      cursor: "0",
      keys: ["new:key"],
      hasMore: false,
    });

    fireEvent.click(screen.getByTitle("query.createRedisKey"));
    fireEvent.change(screen.getByTestId("redis-create-key-input"), {
      target: { value: "new:key" },
    });
    fireEvent.change(screen.getByTestId("redis-create-string-value"), {
      target: { value: "hello" },
    });
    fireEvent.click(screen.getByText("query.createRedisKeySubmit"));

    await waitFor(() => {
      expect(RedisScanKeys).toHaveBeenCalledWith(
        expect.objectContaining({
          assetId: 10,
          db: 0,
          match: "new:key",
          exact: true,
        })
      );
    });
    expect(RedisSetStringValue).not.toHaveBeenCalled();
  });

  it("filters locally while typing and searches Redis on Enter", async () => {
    const matcher = makeLocalKeyMatcher("dispatcher");
    expect(["common:user:1", "dispatcher:task:1"].filter(matcher)).toEqual(["dispatcher:task:1"]);

    vi.useFakeTimers();
    render(<RedisKeyBrowser tabId="query-10" />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(RedisScanKeys).toHaveBeenCalled();
    vi.mocked(RedisScanKeys).mockClear();

    fireEvent.change(screen.getByPlaceholderText("query.filterKeys"), { target: { value: "dispatcher" } });
    await act(async () => {
      vi.advanceTimersByTime(500);
      await Promise.resolve();
    });

    expect(RedisScanKeys).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.keyDown(screen.getByPlaceholderText("query.filterKeys"), { key: "Enter" });
      await Promise.resolve();
    });

    expect(RedisScanKeys).toHaveBeenCalledWith(expect.objectContaining({ match: "dispatcher", exact: true }));
  });

  it("reports a failed key and keeps it listed when delete resolves with a partial failure", async () => {
    // RedisDeleteKeys resolves even when a key failed to delete (e.g. a cluster CLUSTERDOWN on
    // its slot) — it does not reject, so the UI must read the failure from the result.
    vi.mocked(RedisDeleteKeys).mockResolvedValue({
      deleted: 0,
      failed: [{ key: "common:user:1", error: "CLUSTERDOWN" }],
    } as unknown as redis_svc.RedisDeleteResult);

    render(<RedisKeyBrowser tabId="query-10" />);
    fireEvent.click(screen.getByTitle("query.listView"));

    fireEvent.contextMenu(screen.getByText("common:user:1"));
    fireEvent.click(screen.getByText("query.deleteKey"));
    fireEvent.click(screen.getByRole("button", { name: "action.delete" }));

    await waitFor(() => {
      // Redis's own error (e.g. CLUSTERDOWN) must reach the user verbatim, per failed key.
      expect(toast.error).toHaveBeenCalledWith("query.redisDeleteKeysFailed", {
        description: "common:user:1: CLUSTERDOWN",
      });
    });
    expect(screen.getByText("common:user:1")).toBeInTheDocument();
  });

  it("copies the selected key name with Ctrl/Cmd+C", () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const state = useQueryStore.getState().redisStates["query-10"];
    useQueryStore.setState({
      redisStates: { "query-10": { ...state, selectedKey: "common:user:2" } },
    });
    render(<RedisKeyBrowser tabId="query-10" />);

    fireEvent.keyDown(screen.getByTestId("redis-key-tree"), { key: "c", ctrlKey: true, metaKey: true });

    expect(writeText).toHaveBeenCalledWith("common:user:2");
  });

  it("leaves Ctrl/Cmd+C alone when no key is selected", () => {
    const writeText = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<RedisKeyBrowser tabId="query-10" />);

    const ev = new KeyboardEvent("keydown", {
      key: "c",
      ctrlKey: true,
      metaKey: true,
      bubbles: true,
      cancelable: true,
    });
    screen.getByTestId("redis-key-tree").dispatchEvent(ev);

    expect(ev.defaultPrevented).toBe(false);
    expect(writeText).not.toHaveBeenCalled();
  });

  it("leaves Ctrl/Cmd+C to the browser while key text is selected", () => {
    const writeText = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const state = useQueryStore.getState().redisStates["query-10"];
    useQueryStore.setState({
      redisStates: { "query-10": { ...state, selectedKey: "common:user:2" } },
    });
    render(<RedisKeyBrowser tabId="query-10" />);
    fireEvent.click(screen.getByTitle("query.listView"));
    const label = screen.getByText("common:user:2");
    const range = document.createRange();
    range.selectNodeContents(label);
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(range);
    const event = new KeyboardEvent("keydown", {
      key: "c",
      ctrlKey: true,
      metaKey: true,
      bubbles: true,
      cancelable: true,
    });

    label.closest("button")!.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(false);
    expect(writeText).not.toHaveBeenCalled();
  });
});

describe("RedisKeyBrowser cluster mode", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(RedisScanKeys).mockResolvedValue({
      cursor: "0",
      keys: ["user:1", "user:2"],
      hasMore: false,
      scannedMasters: 2,
      totalMasters: 3,
      unreachable: [{ addr: "10.20.0.13:6379", slots: "10923-16383" }],
    });
    vi.mocked(RedisListDatabases).mockResolvedValue([
      {
        db: 0,
        keys: 131,
        expires: 0,
        avgTtl: 0,
        masters: [
          { addr: "10.20.0.11:6379", slots: "0-5460", keys: 62, reachable: true },
          { addr: "10.20.0.12:6379", slots: "5461-10922", keys: 69, reachable: true },
          { addr: "10.20.0.13:6379", slots: "10923-16383", keys: -1, reachable: false },
        ],
      },
    ]);
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
          keys: ["user:1", "user:2"],
          loadingKeys: false,
          keyFilter: "*",
          scanCursor: "0",
          hasMore: false,
          selectedKey: null,
          keyInfo: null,
          dbKeyCounts: { 0: 131 },
          error: null,
          scanNode: "",
          scannedMasters: 2,
          totalMasters: 3,
          unreachableMasters: [{ addr: "10.20.0.13:6379", slots: "10923-16383" }],
        },
      },
    });
  });

  it("replaces the db footer with a scan-range selector and shows partial coverage", async () => {
    render(<RedisKeyBrowser tabId="query-20" />);

    expect(screen.queryByTestId("redis-db-footer")).not.toBeInTheDocument();
    const footer = screen.getByTestId("redis-scan-range-footer");
    expect(footer).toHaveTextContent("query.redisAllMasters");

    const coverage = screen.getByTestId("redis-scan-coverage");
    expect(coverage).toHaveAttribute("data-scanned", "2");
    expect(coverage).toHaveAttribute("data-total", "3");
    expect(coverage).toHaveAttribute("data-partial", "true");
    expect(coverage).toHaveClass("text-warning");
  });

  it("shows an unreachable-master banner with addr and slot range", async () => {
    render(<RedisKeyBrowser tabId="query-20" />);

    const banner = screen.getByTestId("redis-unreachable-banner");
    expect(banner).toBeInTheDocument();
    const node = screen.getByTestId("redis-unreachable-node");
    expect(node).toHaveAttribute("data-addr", "10.20.0.13:6379");
    expect(node).toHaveAttribute("data-slots", "10923-16383");
  });

  it("scans a single master when selected from the scan-range menu", async () => {
    render(<RedisKeyBrowser tabId="query-20" />);
    await waitFor(() => {
      expect(useQueryStore.getState().redisStates["query-20"].masterKeyCounts).toBeDefined();
    });
    vi.mocked(RedisScanKeys).mockClear();

    fireEvent.click(screen.getByRole("button", { name: /query.redisAllMasters/ }));
    const menu = screen.getByTestId("redis-scan-range-menu");
    expect(within(menu).getByText("10.20.0.11:6379")).toBeInTheDocument();
    fireEvent.click(within(menu).getByText("10.20.0.11:6379"));

    await waitFor(() => {
      expect(RedisScanKeys).toHaveBeenCalledWith(
        expect.objectContaining({ assetId: 20, node: "10.20.0.11:6379", cursor: "0" })
      );
    });
  });

  it("hides the database picker in the create-key dialog", async () => {
    render(<RedisKeyBrowser tabId="query-20" />);

    fireEvent.click(screen.getByTitle("query.createRedisKey"));

    expect(screen.queryByText("query.redisDbIndex")).not.toBeInTheDocument();
  });
});
