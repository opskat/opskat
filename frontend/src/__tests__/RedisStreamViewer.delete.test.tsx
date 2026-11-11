import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";

vi.mock("../components/query/useRedisValueVirtualizer", () => ({
  useRedisValueVirtualizer: (count: number) => ({
    scrollRef: { current: null },
    virtualizer: {
      getTotalSize: () => count * 30,
      getVirtualItems: () => Array.from({ length: count }, (_, i) => ({ index: i, key: i, start: i * 30, size: 30 })),
    },
  }),
}));

import { RedisStreamViewer } from "@/components/query/RedisStreamViewer";
import { RedisStreamDelete } from "../../wailsjs/go/redis/Redis";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";

const t = (key: string) => key;

describe("RedisStreamViewer entry deletion", () => {
  beforeEach(() => {
    vi.mocked(RedisStreamDelete)
      .mockReset()
      .mockResolvedValue(undefined as never);
    useTabStore.setState({
      tabs: [
        {
          id: "t1",
          type: "query",
          label: "redis",
          meta: { type: "query", assetId: 3, assetName: "r", assetIcon: "", assetType: "redis" },
        },
      ],
      activeTabId: "t1",
    } as never);
    useQueryStore.setState({
      redisStates: { t1: { selectedKey: "events", currentDb: 0 } },
      selectKey: vi.fn(),
    } as never);
  });

  it("asks for confirmation before XDEL and only deletes after the verb button", async () => {
    const info = {
      type: "stream",
      ttl: -1,
      value: [{ id: "1-0", fields: { a: "b" } }],
      total: 1,
      valueCursor: "",
      valueOffset: 0,
      hasMoreValues: false,
      loadingMore: false,
    };
    render(<RedisStreamViewer info={info as never} tabId="t1" t={t} />);
    fireEvent.click(screen.getByText("1-0"));
    fireEvent.click(screen.getByTitle("query.deleteEntry"));

    const dialog = await screen.findByRole("alertdialog");
    expect(RedisStreamDelete).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "action.delete" }));
    await waitFor(() => expect(RedisStreamDelete).toHaveBeenCalledWith(3, 0, "events", ["1-0"]));
  });

  it("does not delete when the confirm is cancelled", async () => {
    const info = {
      type: "stream",
      ttl: -1,
      value: [{ id: "1-0", fields: { a: "b" } }],
      total: 1,
      valueCursor: "",
      valueOffset: 0,
      hasMoreValues: false,
      loadingMore: false,
    };
    render(<RedisStreamViewer info={info as never} tabId="t1" t={t} />);
    fireEvent.click(screen.getByText("1-0"));
    fireEvent.click(screen.getByTitle("query.deleteEntry"));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "action.cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(RedisStreamDelete).not.toHaveBeenCalled();
  });
});
