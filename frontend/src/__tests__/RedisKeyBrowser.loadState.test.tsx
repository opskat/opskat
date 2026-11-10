import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RedisKeyBrowser } from "../components/query/RedisKeyBrowser";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { RedisListDatabases, RedisScanKeys } from "../../wailsjs/go/redis/Redis";

function setup() {
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
        hasMore: true,
        selectedKey: null,
        keyInfo: null,
        dbKeyCounts: {},
        error: null,
      },
    },
  });
}

describe("RedisKeyBrowser — key list load state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(RedisListDatabases).mockResolvedValue([{ db: 0, keys: 0, expires: 0, avgTtl: 0 }]);
    setup();
  });

  it("shows the scan failure reason with a retry that rescans", async () => {
    vi.mocked(RedisScanKeys).mockRejectedValueOnce(new Error("NOAUTH Authentication required"));
    await act(async () => {
      render(<RedisKeyBrowser tabId="query-10" />);
    });

    const error = await screen.findByTestId("async-state-error");
    expect(error).toHaveTextContent("NOAUTH Authentication required");

    vi.mocked(RedisScanKeys).mockResolvedValueOnce({ cursor: "0", keys: ["user:1"], hasMore: false });
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    await waitFor(() => expect(screen.getByText("user")).toBeInTheDocument());
    expect(screen.queryByTestId("async-state-error")).not.toBeInTheDocument();
  });

  it("shows the empty state after a successful scan that found no keys", async () => {
    vi.mocked(RedisScanKeys).mockResolvedValueOnce({ cursor: "0", keys: [], hasMore: false });
    await act(async () => {
      render(<RedisKeyBrowser tabId="query-10" />);
    });
    expect(await screen.findByTestId("async-state-empty")).toBeInTheDocument();
  });
});
