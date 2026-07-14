import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MainPanel } from "@/components/layout/MainPanel";
import { useAssetStore } from "@/stores/assetStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { useTabStore } from "@/stores/tabStore";
import { asset_entity } from "../../wailsjs/go/models";

const unmounted = vi.fn();

vi.mock("@/components/vnc/VNCPanel", async () => {
  const React = await import("react");
  return {
    VNCPanel: ({ tabId }: { tabId: string }) => {
      React.useEffect(() => () => unmounted(tabId), [tabId]);
      return <div data-testid={`vnc-panel-${tabId}`} />;
    },
  };
});

vi.mock("@/components/settings/SettingsPage", () => ({
  SettingsPage: () => <div data-testid="settings-page" />,
}));

describe("MainPanel VNC lifecycle", () => {
  beforeEach(() => {
    unmounted.mockReset();
    useLayoutStore.setState({ tabBarLayout: "left" });
    useAssetStore.setState({
      assets: [new asset_entity.Asset({ ID: 1, Name: "VNC", Type: "vnc" })],
      initialized: true,
    });
    useTabStore.setState({
      tabs: [
        { id: "vnc-1", type: "page", label: "VNC", meta: { type: "page", pageId: "vnc", assetId: 1 } },
        { id: "settings", type: "page", label: "Settings", meta: { type: "page", pageId: "settings" } },
      ],
      activeTabId: "vnc-1",
    });
  });

  it("keeps a VNC pane mounted while another tab is active", async () => {
    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);
    expect(await screen.findByTestId("vnc-panel-vnc-1")).toBeVisible();

    useTabStore.getState().activateTab("settings");

    expect(await screen.findByTestId("settings-page")).toBeVisible();
    await waitFor(() => expect(screen.getByTestId("vnc-panel-vnc-1")).not.toBeVisible());
    expect(unmounted).not.toHaveBeenCalled();
  });
});
