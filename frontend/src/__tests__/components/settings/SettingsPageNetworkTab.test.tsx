import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { SettingsPage } from "../../../components/settings/SettingsPage";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { GetNetworkProxySettings } from "../../../../wailsjs/go/system/System";

vi.mock("@/components/settings/AISettingsSection", () => ({ AISettingsSection: () => null }));
vi.mock("@/components/settings/ExtensionSection", () => ({ ExtensionSection: () => null }));

describe("SettingsPage network tab", () => {
  it("sits between terminal and external edit and opens the proxy card", async () => {
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
    useSettingsUiStore.setState({ activeTab: "terminal" });
    render(
      <TooltipProvider>
        <SettingsPage />
      </TooltipProvider>
    );
    const names = screen.getAllByRole("tab").map((tab) => tab.textContent);
    const network = names.indexOf("network.title");
    expect(network).toBe(names.indexOf("terminal.title") + 1);
    expect(names[network + 1]).toBe("externalEdit.settings.tab");

    await userEvent.setup().click(screen.getByRole("tab", { name: "network.title" }));
    expect(await screen.findByText("network.proxy.mode")).toBeInTheDocument();
  });
});
