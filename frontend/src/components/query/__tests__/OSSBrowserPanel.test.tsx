import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { OSSListBuckets, OSSUploadObject } from "../../../../wailsjs/go/oss/OSS";
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

  it("clicking upload starts an upload into the current prefix", async () => {
    vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
    vi.mocked(OSSUploadObject).mockResolvedValue([] as never);
    render(<OSSBrowserPanel tabId={TAB} />);
    await screen.findByTestId("oss-bucket-b1");
    // put the tab into a selected-bucket state so the breadcrumb renders
    useOssBrowserStore.setState(
      (s) =>
        ({
          tabs: {
            ...s.tabs,
            [TAB]: {
              ...s.tabs[TAB],
              currentBucket: "b1",
              currentPrefix: "docs/",
              listing: { objects: [], prefixes: [], truncated: false, cursor: "" },
            },
          },
        }) as never
    );
    fireEvent.click(await screen.findByTestId("oss-upload"));
    await waitFor(() => expect(OSSUploadObject).toHaveBeenCalledWith(7, "b1", "docs/"));
  });
});
