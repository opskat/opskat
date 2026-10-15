import { useEffect, useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MainPanel } from "@/components/layout/MainPanel";
import { useExtensionStore } from "@/extension";
import type { ExtManifest } from "@/extension";
import { useLayoutStore } from "@/stores/layoutStore";
import { useTabStore } from "@/stores/tabStore";

/**
 * A page component that, like any real extension page, sets up its state from the
 * asset it was mounted for (the ES page restores that asset's saved consoles).
 */
function AssetPage({ assetId }: { assetId?: number }) {
  const [mountedFor] = useState(assetId);
  return <div data-testid="ext-page">{`mounted for ${mountedFor}, showing ${assetId}`}</div>;
}

/** Asset ids a WorkPage loaded its data for, one entry per load. */
const loads: (number | undefined)[] = [];

/**
 * A page that, like the ES page, loads its asset's data when it mounts and keeps
 * what the user did since (opened tabs, a query and its results) in its own state.
 */
function WorkPage({ assetId }: { assetId?: number }) {
  const [opened, setOpened] = useState(0);
  useEffect(() => {
    loads.push(assetId);
  }, [assetId]);
  return (
    <button data-testid="ext-page" onClick={() => setOpened((n) => n + 1)}>
      {`asset ${assetId}, opened ${opened}`}
    </button>
  );
}

const manifest: ExtManifest = {
  name: "demo",
  version: "1.0.0",
  icon: "",
  i18n: { displayName: "Demo", description: "" },
  frontend: {
    entry: "index.js",
    styles: "",
    pages: [
      { id: "main", slot: "asset.connect", i18n: { name: "Main" }, component: "AssetPage" },
      { id: "work", slot: "asset.connect", i18n: { name: "Work" }, component: "WorkPage" },
    ],
  },
};

function pageTab(id: string, assetId: number, pageId = "main") {
  return {
    id,
    type: "page" as const,
    label: id,
    meta: { type: "page" as const, pageId, extensionName: "demo", assetId },
  };
}

/** The page the user is looking at: the one in the active tab. */
function shownPage() {
  const shown = screen
    .getAllByTestId("ext-page")
    .filter((el) => el.closest<HTMLElement>("[style]")?.style.display !== "none");
  expect(shown).toHaveLength(1);
  return shown[0];
}

describe("MainPanel extension page tabs", () => {
  beforeEach(() => {
    useLayoutStore.setState({ tabBarLayout: "left" });
    useExtensionStore.setState({
      ready: true,
      disabled: {},
      extensions: {
        demo: { manifest, loaded: { name: "demo", manifest, components: { AssetPage, WorkPage } } },
      },
    });
    useTabStore.setState({ tabs: [pageTab("page-a", 1), pageTab("page-b", 2)], activeTabId: "page-a" });
    loads.length = 0;
  });

  it("gives each asset's page tab its own page instance", async () => {
    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);
    await screen.findAllByTestId("ext-page");
    expect(shownPage()).toHaveTextContent("mounted for 1, showing 1");

    act(() => useTabStore.getState().activateTab("page-b"));
    expect(shownPage()).toHaveTextContent("mounted for 2, showing 2");

    act(() => useTabStore.getState().activateTab("page-a"));
    expect(shownPage()).toHaveTextContent("mounted for 1, showing 1");
  });

  it("keeps a page as the user left it while another tab is active, without loading it again", async () => {
    useTabStore.setState({
      tabs: [pageTab("work-a", 1, "work"), pageTab("work-b", 2, "work")],
      activeTabId: "work-a",
    });
    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);
    await screen.findAllByTestId("ext-page");
    fireEvent.click(shownPage());
    expect(shownPage()).toHaveTextContent("asset 1, opened 1");

    act(() => useTabStore.getState().activateTab("work-b"));
    expect(shownPage()).toHaveTextContent("asset 2, opened 0");

    act(() => useTabStore.getState().activateTab("work-a"));
    expect(shownPage()).toHaveTextContent("asset 1, opened 1");
    expect(loads).toEqual([1, 2]);
  });
});
