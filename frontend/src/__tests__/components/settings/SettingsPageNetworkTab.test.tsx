import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { SettingsPage } from "../../../components/settings/SettingsPage";
import { useSettingsUiStore, type SettingsTab } from "@/stores/settingsUiStore";
import { GetAvailableMirrors, GetDownloadMirror, GetNetworkProxySettings } from "../../../../wailsjs/go/system/System";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";

vi.mock("@/components/settings/AISettingsSection", () => ({ AISettingsSection: () => null }));
vi.mock("@/components/settings/ExtensionSection", () => ({ ExtensionSection: () => null }));

function renderPage(tab: SettingsTab) {
  vi.mocked(EventsOn).mockReturnValue(vi.fn());
  vi.mocked(GetNetworkProxySettings).mockResolvedValue({
    mode: "env",
    type: "http",
    host: "",
    port: 0,
    username: "",
    passwordSet: false,
    noProxy: "",
    envVars: [],
  } as never);
  useSettingsUiStore.setState({ activeTab: tab });
  render(
    <TooltipProvider>
      <SettingsPage />
    </TooltipProvider>
  );
  return within(screen.getByRole("navigation", { name: "settings.nav.label" }));
}

describe("SettingsPage grouped navigation", () => {
  it("lists five groups with the network item between terminal and external edit", () => {
    const nav = renderPage("terminal");
    expect(nav.getAllByRole("group").map((g) => g.getAttribute("aria-label"))).toEqual([
      "settings.group.general",
      "settings.group.connection",
      "settings.group.ai",
      "settings.group.data",
      "settings.group.about",
    ]);
    const names = nav.getAllByRole("button").map((b) => b.textContent);
    const network = names.indexOf("settings.nav.network");
    expect(network).toBe(names.indexOf("terminal.title") + 1);
    expect(names[network + 1]).toBe("externalEdit.settings.tab");
  });

  it("opens the proxy card from the network item", async () => {
    const nav = renderPage("terminal");
    await userEvent.setup().click(nav.getByRole("button", { name: "settings.nav.network" }));
    expect(await screen.findByText("network.proxy.mode")).toBeInTheDocument();
    expect(nav.getByRole("button", { name: "settings.nav.network" })).toHaveAttribute("aria-current", "page");
  });

  it("marks the item that owns a legacy tab id as current", () => {
    const nav = renderPage("backup");
    expect(nav.getByRole("button", { name: "settings.nav.importBackup" })).toHaveAttribute("aria-current", "page");
  });

  it("lists update, system status and debugging/logs under about", () => {
    const nav = renderPage("about");
    const about = nav.getByRole("group", { name: "settings.group.about" });
    expect(
      within(about)
        .getAllByRole("button")
        .map((b) => b.textContent)
    ).toEqual(["settings.nav.update", "systemStatus.title", "settings.nav.debugLogs"]);
    expect(
      nav
        .getAllByRole("button")
        .map((b) => b.textContent)
        .slice(0, 1)
    ).toEqual(["settings.nav.appearance"]);
  });

  it("moves debug mode, bug report and logs folder out of the update item into debugging/logs", async () => {
    vi.mocked(GetDownloadMirror).mockResolvedValue("");
    vi.mocked(GetAvailableMirrors).mockResolvedValue([]);
    const nav = renderPage("about");
    expect(screen.queryByText("appUpdate.debugMode")).toBeNull();
    expect(screen.queryByRole("button", { name: "appUpdate.openLogsDir" })).toBeNull();
    await userEvent.setup().click(nav.getByRole("button", { name: "settings.nav.debugLogs" }));
    expect(screen.getByText("appUpdate.debugMode")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "appUpdate.openLogsDir" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "appUpdate.reportBug" })).toBeInTheDocument();
  });

  it("shows both import and backup content under the merged item", () => {
    renderPage("import");
    expect(screen.getByText("import.tabbyDesc")).toBeInTheDocument();
    expect(screen.getByText("backup.fileDesc")).toBeInTheDocument();
  });
});
