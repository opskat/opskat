import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MainPanel } from "@/components/layout/MainPanel";
import { useLayoutStore } from "@/stores/layoutStore";
import { useSFTPStore } from "@/stores/sftpStore";
import { useTabStore } from "@/stores/tabStore";
import { profiled } from "./renderCount";

describe("MainPanel render isolation", () => {
  it("does not re-render on sftp transfer progress", async () => {
    useLayoutStore.setState({ tabBarLayout: "left" });
    useTabStore.setState({ tabs: [], activeTabId: null });
    const { tree, counter } = profiled(
      <MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />
    );
    await act(async () => {
      render(tree);
    });
    const before = counter.commits;
    await act(async () => {
      useSFTPStore.setState({
        transfers: {
          t1: {
            transferId: "t1",
            tabId: "tab-1",
            sessionId: "s1",
            direction: "upload",
            currentFile: "a",
            filesCompleted: 0,
            filesTotal: 1,
            bytesDone: 10,
            bytesTotal: 100,
            speed: 1,
            status: "active",
          },
        },
      });
    });
    expect(counter.commits).toBe(before);
  });
});
