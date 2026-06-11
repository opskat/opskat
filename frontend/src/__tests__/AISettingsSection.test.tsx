import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AISettingsSection } from "@/components/settings/AISettingsSection";
import { DetectOpsctl } from "../../wailsjs/go/system/System";
import {
  DetectSkills,
  GetAppVersion,
  GetDataDir,
  GetOpsctlInstallDir,
  UninstallSkill,
} from "../../wailsjs/go/system/System";
import { ListAIProviders } from "../../wailsjs/go/ai/AI";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";

describe("AISettingsSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ListAIProviders).mockResolvedValue([]);
    vi.mocked(DetectOpsctl).mockResolvedValue({
      installed: false,
      path: "",
      version: "",
      embedded: false,
    });
    vi.mocked(DetectSkills).mockResolvedValue([]);
    vi.mocked(GetOpsctlInstallDir).mockResolvedValue("");
    vi.mocked(GetDataDir).mockResolvedValue("");
    vi.mocked(GetAppVersion).mockResolvedValue("dev");
  });

  it("opens the GitHub Releases page for manual opsctl CLI install", async () => {
    render(<AISettingsSection />);

    await userEvent.click(screen.getByRole("button", { name: /GitHub Releases/i }));

    expect(BrowserOpenURL).toHaveBeenCalledWith("https://github.com/opskat/opskat/releases");
  });

  it("uninstalls a single installed AI plugin target", async () => {
    vi.mocked(DetectSkills)
      .mockResolvedValueOnce([
        { key: "codex", name: "Codex", installed: true, path: "C:/Users/test/.codex/skills/opsctl" },
      ])
      .mockResolvedValueOnce([
        { key: "codex", name: "Codex", installed: false, path: "C:/Users/test/.codex/skills/opsctl" },
      ]);

    render(<AISettingsSection />);

    expect(await screen.findAllByText("Codex")).toHaveLength(2);

    await userEvent.click(screen.getByRole("button", { name: "integration.skillUninstall" }));
    await userEvent.click(screen.getAllByRole("button", { name: "integration.skillUninstall" }).at(-1)!);

    await waitFor(() => expect(UninstallSkill).toHaveBeenCalledWith("codex"));
    await waitFor(() => expect(DetectSkills).toHaveBeenCalledTimes(2));
  });
});
