import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MainPanel } from "@/components/layout/MainPanel";
import { useAssetStore } from "@/stores/assetStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useTabStore } from "@/stores/tabStore";
import { asset_entity } from "../../wailsjs/go/models";

const unmounted = vi.fn();

vi.mock("@/components/rdp/RDPPanel", async () => {
  const React = await import("react");
  return {
    RDPPanel: ({ asset }: { asset: asset_entity.Asset }) => {
      React.useEffect(() => () => unmounted(asset.ID), [asset.ID]);
      return <div data-testid={`rdp-panel-${asset.ID}`} />;
    },
  };
});

vi.mock("@/components/settings/SettingsPage", () => ({
  SettingsPage: () => <div data-testid="settings-page" />,
}));

describe("MainPanel RDP lifecycle", () => {
  beforeEach(() => {
    unmounted.mockReset();
    useLayoutStore.setState({ tabBarLayout: "left" });
    useAssetStore.setState({
      assets: [new asset_entity.Asset({ ID: 1, Name: "RDP", Type: "rdp" })],
      initialized: true,
    });
    useTabStore.setState({
      tabs: [
        { id: "rdp-1", type: "page", label: "RDP", meta: { type: "page", pageId: "rdp", assetId: 1 } },
        { id: "settings", type: "page", label: "Settings", meta: { type: "page", pageId: "settings" } },
      ],
      activeTabId: "rdp-1",
    });
  });

  it("keeps an RDP pane mounted while another tab is active", async () => {
    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);
    expect(await screen.findByTestId("rdp-panel-1")).toBeVisible();

    useTabStore.getState().activateTab("settings");

    expect(await screen.findByTestId("settings-page")).toBeVisible();
    await waitFor(() => expect(screen.getByTestId("rdp-panel-1")).not.toBeVisible());
    expect(unmounted).not.toHaveBeenCalled();
  });
});
