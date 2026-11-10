import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MainPanel } from "@/components/layout/MainPanel";
import { useAssetStore } from "@/stores/assetStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { openSettingsTab, useTabStore } from "@/stores/tabStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";

vi.mock("@/components/settings/SettingsPage", () => ({
  SettingsPage: () => <div data-testid="settings-page" />,
}));

describe("settings entry points land on their item", () => {
  beforeEach(() => {
    useSettingsUiStore.setState({ activeTab: "ai" });
    useLayoutStore.setState({ tabBarLayout: "top" });
    useAssetStore.setState({ assets: [], initialized: true });
    useTabStore.setState({ tabs: [], activeTabId: null });
  });

  it("openSettingsTab with an item opens settings on that item", () => {
    openSettingsTab("Settings", "status");
    expect(useTabStore.getState().activeTabId).toBe("settings");
    expect(useSettingsUiStore.getState().activeTab).toBe("status");
  });

  it("openSettingsTab without an item keeps the last item", () => {
    useSettingsUiStore.setState({ activeTab: "network" });
    openSettingsTab("Settings");
    expect(useSettingsUiStore.getState().activeTab).toBe("network");
  });

  it("home page 'all shortcuts' link opens the shortcuts item", () => {
    render(<MainPanel onEditAsset={vi.fn()} onDeleteAsset={vi.fn()} onConnectAsset={vi.fn()} />);
    fireEvent.click(screen.getByText(/app\.shortcuts\.all/));
    expect(useTabStore.getState().activeTabId).toBe("settings");
    expect(useSettingsUiStore.getState().activeTab).toBe("shortcuts");
  });
});
