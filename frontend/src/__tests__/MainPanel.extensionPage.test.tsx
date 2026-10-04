import { useState } from "react";
import { act, render, screen } from "@testing-library/react";
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

const manifest: ExtManifest = {
  name: "demo",
  version: "1.0.0",
  icon: "",
  i18n: { displayName: "Demo", description: "" },
  frontend: {
    entry: "index.js",
    styles: "",
    pages: [{ id: "main", slot: "asset.connect", i18n: { name: "Main" }, component: "AssetPage" }],
  },
};

function pageTab(id: string, assetId: number) {
  return {
    id,
    type: "page" as const,
    label: id,
    meta: { type: "page" as const, pageId: "main", extensionName: "demo", assetId },
  };
}

describe("MainPanel extension page tabs", () => {
  beforeEach(() => {
    useLayoutStore.setState({ tabBarLayout: "left" });
    useExtensionStore.setState({
      ready: true,
      disabled: {},
      extensions: {
        demo: { manifest, loaded: { name: "demo", manifest, components: { AssetPage } } },
      },
    });
    useTabStore.setState({ tabs: [pageTab("page-a", 1), pageTab("page-b", 2)], activeTabId: "page-a" });
  });

  it("gives each asset's page tab its own page instance", async () => {
    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);
    expect(await screen.findByTestId("ext-page")).toHaveTextContent("mounted for 1, showing 1");

    act(() => useTabStore.getState().activateTab("page-b"));
    expect(screen.getByTestId("ext-page")).toHaveTextContent("mounted for 2, showing 2");

    act(() => useTabStore.getState().activateTab("page-a"));
    expect(screen.getByTestId("ext-page")).toHaveTextContent("mounted for 1, showing 1");
  });
});
