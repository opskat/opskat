import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { SettingsPage } from "../../../components/settings/SettingsPage";
import { useSettingsUiStore, type SettingsTab } from "@/stores/settingsUiStore";
import { GetNetworkProxySettings } from "../../../../wailsjs/go/system/System";

vi.mock("@/components/settings/AISettingsSection", () => ({ AISettingsSection: () => null }));
vi.mock("@/components/settings/ExtensionSection", () => ({ ExtensionSection: () => null }));

function renderPage(tab: SettingsTab) {
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
    const network = names.indexOf("network.title");
    expect(network).toBe(names.indexOf("terminal.title") + 1);
    expect(names[network + 1]).toBe("externalEdit.settings.tab");
  });

  it("opens the proxy card from the network item", async () => {
    const nav = renderPage("terminal");
    await userEvent.setup().click(nav.getByRole("button", { name: "network.title" }));
    expect(await screen.findByText("network.proxy.mode")).toBeInTheDocument();
    expect(nav.getByRole("button", { name: "network.title" })).toHaveAttribute("aria-current", "page");
  });

  it("marks the item that owns a legacy tab id as current", () => {
    const nav = renderPage("backup");
    expect(nav.getByRole("button", { name: "settings.nav.importBackup" })).toHaveAttribute("aria-current", "page");
  });

  it("shows both import and backup content under the merged item", () => {
    renderPage("import");
    expect(screen.getByText("import.tabbyDesc")).toBeInTheDocument();
    expect(screen.getByText("backup.fileDesc")).toBeInTheDocument();
  });
});
