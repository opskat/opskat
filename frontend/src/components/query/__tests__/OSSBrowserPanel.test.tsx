import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { OSSListBuckets } from "../../../../wailsjs/go/oss/OSS";
import { OSSBrowserPanel } from "../OSSBrowserPanel";
import { useTabStore } from "@/stores/tabStore";
import { useOssBrowserStore } from "@/stores/ossBrowserStore";

const TAB = "query-7";

beforeEach(() => {
  vi.mocked(OSSListBuckets).mockReset();
  useOssBrowserStore.setState({ tabs: {} });
  useTabStore.setState({
    tabs: [
      {
        id: TAB,
        type: "query",
        label: "b",
        meta: { type: "query", assetId: 7, assetName: "b", assetIcon: "", assetType: "oss" },
      },
    ],
    activeTabId: TAB,
  });
});

describe("OSSBrowserPanel", () => {
  it("loads buckets on mount and renders them in the tree", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await waitFor(() => expect(OSSListBuckets).toHaveBeenCalledWith(7));
    expect(await screen.findByTestId("oss-bucket-b1")).toBeInTheDocument();
    expect(screen.getByTestId("oss-browser-panel")).toBeInTheDocument();
  });

  it("guards against a missing asset id", () => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    render(<OSSBrowserPanel tabId="nope" />);
    expect(screen.getByText("oss.browser.missingAsset")).toBeInTheDocument();
  });
});
